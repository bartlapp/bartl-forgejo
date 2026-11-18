#!/usr/bin/env node
// SPDX-License-Identifier: Apache-2.0
import { spawn } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import { mkdir, writeFile, readFile, chmod } from 'node:fs/promises';
import { isAbsolute, join } from 'node:path';
import { isIP } from 'node:net';

const root = process.env.LAB_QUALIFICATION_DIR || '/home/runner/qualification';
const host = process.env.BARTL_FORGEJO_HOST;
const targetHost = process.env.BARTL_FORGEJO_TARGET_HOST;
const keycloakHost = process.env.BARTL_KEYCLOAK_HOST;
const address = process.env.BARTL_LAB_IPV4;
const resticChart = join(root, 'bartl-restic-0.3.0.tgz');
const keycloakChart = join(root, 'bartl-keycloak-0.6.1.tgz');
const namespaces = ['forgejo-blue', 'forgejo-green', 'forgejo-empty', 'forgejo-backup', 'forgejo-keycloak'];
const backendHost = 'backup-restic.forgejo-backup.svc.cluster.local';
const backupURL = `rest:https://${backendHost}/forgejo/`;
let sequence = 0;
const secret = () => randomBytes(32).toString('hex');
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
async function file(path, content) {
  await writeFile(path, content, { mode: 0o600, flag: 'wx' });
}
async function command(program, args, input = undefined, { allowFailure = false } = {}) {
  const id = String(++sequence).padStart(4, '0');
  const result = await new Promise((resolve, reject) => {
    const child = spawn(program, args, { stdio: ['pipe', 'pipe', 'pipe'] });
    const out = [], err = [];
    child.stdout.on('data', x => out.push(x));
    child.stderr.on('data', x => err.push(x));
    child.on('error', reject);
    child.on('close', code => resolve({ code, out: Buffer.concat(out), err: Buffer.concat(err) }));
    child.stdin.end(input);
  });
  await file(join(root, 'private-logs', `${id}.stdout`), result.out);
  await file(join(root, 'private-logs', `${id}.stderr`), result.err);
  if (result.code !== 0 && !allowFailure) throw new Error(`Setup command ${id} (${program}) failed; inspect protected private-logs`);
  return result;
}
const kubectl = (...args) => command('kubectl', args);
async function apply(object) { await command('kubectl', ['apply', '-f', '-'], JSON.stringify(object)); }
async function ksecret(namespace, name, data, type = 'Opaque') {
  await command('kubectl', ['apply', '--server-side', '--field-manager=forgejo-preparation', '-f', '-'],
    JSON.stringify({ apiVersion: 'v1', kind: 'Secret', metadata: { namespace, name }, type,
      data: Object.fromEntries(Object.entries(data).map(([key, value]) => [key, Buffer.from(value).toString('base64')])) }));
}
async function pvc(namespace, name, size) {
  await apply({ apiVersion: 'v1', kind: 'PersistentVolumeClaim', metadata: { namespace, name },
    spec: { accessModes: ['ReadWriteOnce'], resources: { requests: { storage: size } } } });
}
async function ready(namespace, resource, timeout = '30m') {
  await kubectl('-n', namespace, 'wait', resource, '--for=condition=Ready', `--timeout=${timeout}`);
  const { out } = await kubectl('-n', namespace, 'get', resource, '-o', 'json');
  const object = JSON.parse(out);
  if (object.status?.observedGeneration !== object.metadata.generation || !object.status.conditions?.some(c => c.type === 'Ready' && c.status === 'True' && c.observedGeneration === object.metadata.generation)) {
    throw new Error(`${resource} readiness is stale`);
  }
}
async function dns() {
  for (const name of [host, targetHost, keycloakHost]) {
    for (const resolver of ['ns1.desec.io', 'ns2.desec.org', '1.1.1.1', '8.8.8.8']) {
      const { out } = await command('dig', [`@${resolver}`, name, 'A', '+short']);
      if (out.toString().trim() !== address) throw new Error(`DNS ${name} on ${resolver} must resolve only to ${address}; wait for propagation`);
      if (resolver.startsWith('ns')) {
        const authoritative = await command('dig', [`@${resolver}`, name, 'A', '+norecurse', '+noall', '+comments', '+answer']);
        if (!/flags:[^;\n]*\baa\b/.test(authoritative.out.toString())) throw new Error(`DNS ${name} on ${resolver} is not authoritative`);
      }
      const ipv6 = await command('dig', [`@${resolver}`, name, 'AAAA', '+short']);
      if (ipv6.out.toString().trim()) throw new Error(`Unexpected AAAA record for ${name}; review routing before issuance`);
    }
  }
}
async function releasedChart(name, version, checksum) {
  await command('helm', ['pull', `oci://ghcr.io/bartlapp/${name}`, '--version', version, '--destination', root]);
  const archive = await readFile(join(root, `${name}-${version}.tgz`));
  if (createHash('sha256').update(archive).digest('hex') !== checksum) throw new Error(`${name} released archive checksum mismatch`);
}
async function declaration(chart, release, namespace, values, mode = 'connections') {
  const rendered = await command('helm', ['template', release, chart, '-n', namespace,
    '-f', '-', '--set-string', `renderMode=${mode}`, '--show-only', `templates/${mode}.yaml`], JSON.stringify(values));
  const decoded = await command('yq', ['-o=json', '.'], rendered.out);
  return JSON.parse(decoded.out);
}
async function backend() {
  const dir = join(root, 'backup');
  await mkdir(dir, { mode: 0o700 });
  const path = name => join(dir, name);
  await command('openssl', ['req', '-x509', '-newkey', 'rsa:3072', '-nodes', '-days', '14', '-subj', '/CN=Forgejo disposable lab CA', '-keyout', path('ca.key'), '-out', path('ca.crt'), '-addext', 'basicConstraints=critical,CA:TRUE']);
  await command('openssl', ['req', '-new', '-newkey', 'rsa:3072', '-nodes', '-subj', `/CN=${backendHost}`, '-keyout', path('tls.key'), '-out', path('tls.csr')]);
  await file(path('extensions'), `subjectAltName=DNS:${backendHost},DNS:backup-restic.forgejo-backup.svc\nbasicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\n`);
  await command('openssl', ['x509', '-req', '-in', path('tls.csr'), '-CA', path('ca.crt'), '-CAkey', path('ca.key'), '-CAcreateserial', '-days', '14', '-extfile', path('extensions'), '-out', path('tls.crt')]);
  const password = secret();
  await file(path('password'), password);
  let htpasswd = '';
  const credentials = {};
  const ca = await readFile(path('ca.crt'));
  for (const role of ['reader', 'writer', 'admin']) {
    const restPassword = secret();
    const hashed = await command('openssl', ['passwd', '-apr1', '-stdin'], `${restPassword}\n`);
    htpasswd += `${role}:${hashed.out.toString().trim()}\n`;
    credentials[role] = { username: role, restPassword, password, 'ca.crt': ca };
    await file(path(`${role}.json`), JSON.stringify({ username: role, restPassword, password, ca: ca.toString() }, null, 2));
    await file(path(`${role}-secret.json`), JSON.stringify({ apiVersion: 'v1', kind: 'Secret', type: 'Opaque',
      metadata: { name: `forgejo-backup-${role}` },
      data: Object.fromEntries(Object.entries(credentials[role]).map(([key, value]) => [key, Buffer.from(value).toString('base64')])) }, null, 2));
  }
  await pvc('forgejo-backup', 'repository', '15Gi');
  await ksecret('forgejo-backup', 'repository-tls', { 'tls.crt': await readFile(path('tls.crt')), 'tls.key': await readFile(path('tls.key')) }, 'kubernetes.io/tls');
  await ksecret('forgejo-backup', 'forgejo-repository-auth', { htpasswd });
  await ksecret('forgejo-backup', 'repository-key', { password });
  await ksecret('forgejo-backup', 'repository-admin', credentials.admin);
  const values = { installation: 'forgejo-qualification', instance: 'restic', services: { restic: {
    release: '0.3.0', hostname: backendHost, replicas: 1, existingClaim: 'repository', tlsSecret: 'repository-tls',
    tls: { managed: false }, ingress: { enabled: false },
    repositories: [{ name: 'forgejo', authSecret: 'forgejo-repository-auth', passwordSecret: 'repository-key' }] } } };
  await file(path('values.json'), JSON.stringify(values, null, 2));
  await command('helm', ['upgrade', '--install', 'backup', resticChart, '-n', 'forgejo-backup', '-f', path('values.json'), '--wait=legacy', '--timeout=5m']);
  await ready('forgejo-backup', 'resticapplication/backup-restic');
  return credentials;
}
async function keycloak() {
  const namespace = 'forgejo-keycloak';
  const password = secret();
  await ksecret(namespace, 'keycloak-database', { username: 'keycloak', database: 'keycloak', password: secret() });
  await ksecret(namespace, 'keycloak-database-admin', { username: 'postgres', password: secret() });
  await ksecret(namespace, 'keycloak-bootstrap', { username: 'admin', password });
  await file(join(root, 'keycloak-admin.json'), JSON.stringify({ username: 'admin', password }));
  const values = { installation: 'forgejo-qualification', instance: 'keycloak', services: { keycloak: {
    release: '0.6.1', hostname: keycloakHost,
    database: { secretName: 'keycloak-database', adminSecretName: 'keycloak-database-admin', size: '5Gi' }, bootstrapAdminSecret: 'keycloak-bootstrap',
    ingress: { enabled: true, className: 'traefik', tlsSecretName: 'keycloak-tls', controllerNamespace: 'kube-system', administratorSourceRanges: [] } } } };
  await file(join(root, 'keycloak.values.json'), JSON.stringify(values, null, 2));
  await command('helm', ['upgrade', '--install', 'keycloak', keycloakChart, '-n', namespace, '-f', join(root, 'keycloak.values.json'), '--wait=legacy', '--timeout=5m']);
  // Native management API, not a substitute login service. Retry startup only;
  // mutating requests below are not blindly retried after uncertain responses.
  const provider = await declaration(keycloakChart, 'keycloak', namespace, values);
  const base = provider.services.keycloak.endpoints.https.url;
  const issuer = provider.services.keycloak.oidc.issuer;
  const consumers = [];
  for (const hostname of [host, targetHost]) {
    const choice = { installation: values.installation, instance: 'forgejo',
      services: { forgejo: { hostname, oidc: { issuer, name: 'Keycloak' } } } };
    const connection = await declaration(process.env.BARTL_FORGEJO_CHART, 'forgejo', namespace, choice);
    const configuration = await declaration(process.env.BARTL_FORGEJO_CHART, 'forgejo', namespace, choice, 'configuration');
    consumers.push({ ...connection.oidc.requests.forgejo, ...configuration.configuration.forgejo.oidc });
  }
  let token;
  for (let attempt = 0; attempt < 180; attempt++) {
    try {
      const response = await fetch(`${base}/realms/master/protocol/openid-connect/token`, { method: 'POST',
        body: new URLSearchParams({ grant_type: 'password', client_id: 'admin-cli', username: 'admin', password }), signal: AbortSignal.timeout(10000) });
      if (response.ok) { token = (await response.json()).access_token; break; }
    } catch { /* Startup may not have a public route yet. */ }
    await pause(5000);
  }
  if (!token) throw new Error('Keycloak HTTPS/admin API unavailable; inspect protected inputs and workload status');
  const realm = issuer.split('/').at(-1);
  const clientSecret = secret();
  const userPassword = `Lab-9!${secret()}`;
  const response = await fetch(`${base}/admin/realms`, { method: 'POST', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({ realm, enabled: true, registrationAllowed: false, resetPasswordAllowed: false,
      clients: [{ clientId: consumers[0].clientId, protocol: 'openid-connect', enabled: true, publicClient: false, secret: clientSecret,
        standardFlowEnabled: true, directAccessGrantsEnabled: false,
        redirectUris: consumers.flatMap(consumer => consumer.redirectUris),
        webOrigins: consumers.flatMap(consumer => consumer.webOrigins) }],
      users: [{ username: 'oidc-proof', enabled: true, email: 'oidc-proof@example.invalid', emailVerified: true, firstName: 'OIDC', lastName: 'Proof',
        credentials: [{ type: 'password', value: userPassword, temporary: false }] }] }), signal: AbortSignal.timeout(30000) });
  if (response.status !== 201) throw new Error(`Disposable Keycloak realm creation returned HTTP ${response.status}`);
  const oidc = { providerName: consumers[0].name, clientId: consumers[0].clientId, clientSecret, issuer,
    discoveryURL: consumers[0].discoveryUrl, callbackURL: consumers[0].callbackUrl, username: 'oidc-proof', password: userPassword };
  await file(join(root, 'oidc.json'), JSON.stringify(oidc, null, 2));
  return { issuer: oidc.issuer, configuration: join(root, 'oidc.json') };
}
async function nativeConfiguration(namespace, valuesPath, identity) {
  const rendered = await command('helm', ['template', 'forgejo', process.env.BARTL_FORGEJO_CHART,
    '-n', namespace, '-f', valuesPath, '--set-string', 'instance=forgejo',
    '--set-string', 'renderMode=configuration', '--show-only', 'templates/configuration.yaml']);
  const decoded = await command('yq', ['-o=json', '.configuration.forgejo'], rendered.out);
  const configuration = JSON.parse(decoded.out);
  return `${configuration.appIni}\n[security]\nSECRET_KEY = ${identity.secret}\nINTERNAL_TOKEN = ${identity.internal}\n[server]\nLFS_JWT_SECRET = ${identity.lfs}\n[service]\nREQUIRE_SIGNIN_VIEW = true\n`;
}
async function main() {
  if (process.platform !== 'linux' || process.env.BARTL_FORGEJO_LAB !== 'hetzner' || process.env.BARTL_FORGEJO_DISPOSABLE_CLUSTER !== 'yes') throw new Error('Explicit disposable Linux Hetzner opt-in required');
  const hostname = value => typeof value === 'string' && value.length <= 253 && value.includes('.') &&
    value.split('.').every(label => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label));
  if (![host, targetHost, keycloakHost].every(hostname) || new Set([host, targetHost, keycloakHost]).size !== 3) {
    throw new Error('Set distinct valid BARTL_FORGEJO_HOST, BARTL_FORGEJO_TARGET_HOST and BARTL_KEYCLOAK_HOST DNS names');
  }
  if (!address || isIP(address) !== 4) throw new Error('Set BARTL_LAB_IPV4 to the disposable lab public IPv4 address');
  if (!isAbsolute(root)) throw new Error('LAB_QUALIFICATION_DIR must be an absolute remote path');
  if (!isAbsolute(process.env.BARTL_FORGEJO_CHART || '')) throw new Error('BARTL_FORGEJO_CHART must identify the retained 0.2.1 chart archive by absolute path');
  process.umask(0o077);
  // This is a fresh-setup tool, not a destructive reset or reconciliation tool.
  // Existing directory/namespace causes refusal, never deletion or credential rotation.
  await mkdir(root, { mode: 0o700 });
  await chmod(root, 0o700);
  await mkdir(join(root, 'private-logs'), { mode: 0o700 });
  for (const ns of namespaces) {
    const result = await command('kubectl', ['get', 'namespace', ns, '--ignore-not-found', '-o', 'name']);
    if (result.out.length) throw new Error(`Namespace ${ns} already exists; fresh lab required`);
  }
  await dns();
  await kubectl('-n', 'cert-manager', 'rollout', 'status', 'deployment/cert-manager', '--timeout=5m');
  await releasedChart('bartl-restic', '0.3.0', '57feb5b39b325e3972fe9a9c870cd6c0c5fe54f2f91faba8b4b1652fb4918253');
  await releasedChart('bartl-keycloak', '0.6.1', '7abb55bcefbcde70dc8f29309d508d314a48783547067b529d48617d23c9225a');
  for (const namespace of namespaces) await apply({ apiVersion: 'v1', kind: 'Namespace', metadata: { name: namespace } });
  const roles = await backend();
  const oidc = await keycloak();
  const recoveryIdentity = { secret: secret(), internal: secret(), lfs: randomBytes(32).toString('base64url') };
  await file(join(root, 'recovery-identity.json'), JSON.stringify(recoveryIdentity));
  const bootstrap = { username: 'proof-admin', password: `Lab-9!${secret()}`, email: 'proof-admin@example.invalid' };
  await file(join(root, 'bootstrap.json'), JSON.stringify(bootstrap));
  const slots = {};
  for (const [index, slot] of ['blue', 'green', 'empty'].entries()) {
    const namespace = `forgejo-${slot}`;
    const dir = join(root, slot);
    await mkdir(dir, { mode: 0o700 });
    const slotHost = slot === 'green' ? targetHost : host;
    await pvc(namespace, 'forgejo-state', '8Gi');
    await pvc(namespace, 'forgejo-capture', '8Gi');
    await ksecret(namespace, 'forgejo-bootstrap', bootstrap);
    await ksecret(namespace, 'forgejo-backup-reader', roles.reader);
    await ksecret(namespace, 'forgejo-backup-writer', roles.writer);
    const values = { installation: 'forgejo-qualification', instance: 'forgejo', services: { forgejo: {
      release: '0.2.1', hostname: slotHost, version: '16.0.7', replicas: 1,
      claims: { state: 'forgejo-state', staging: 'forgejo-capture' }, configurationSecret: 'forgejo-configuration', bootstrapSecret: 'forgejo-bootstrap',
      backup: { repository: backupURL, readerSecret: 'forgejo-backup-reader', writerSecret: 'forgejo-backup-writer' },
      oidc: { issuer: oidc.issuer, name: 'Keycloak' },
      ingress: { enabled: slot !== 'empty', className: 'traefik', tlsSecret: 'forgejo-tls' },
      ssh: { port: 2222, exposure: 'NodePort', nodePort: 30222 + index } } } };
    await file(join(dir, 'values.json'), JSON.stringify(values, null, 2));
    const config = await nativeConfiguration(namespace, join(dir, 'values.json'), recoveryIdentity);
    await file(join(dir, 'app.ini'), config);
    await ksecret(namespace, 'forgejo-configuration', { 'app.ini': config });
    slots[slot] = { namespace, release: 'forgejo', host: slotHost, url: `https://${slotHost}`,
      values: join(dir, 'values.json'), configuration: join(dir, 'app.ini'), sshNodePort: 30222 + index };
  }
  const result = { root, host, address, proxyHost: address, url: `https://${host}`, slots, bootstrap: join(root, 'bootstrap.json'), oidc,
    backup: { namespace: 'forgejo-backup', service: 'backup-restic', claim: 'repository', repository: 'forgejo', url: backupURL,
      port: 443, repositoryPath: '/forgejo/', tlsServerName: backendHost,
      readerSecret: join(root, 'backup/reader-secret.json'), writerSecret: join(root, 'backup/writer-secret.json'),
      reader: join(root, 'backup/reader.json'), writer: join(root, 'backup/writer.json'), admin: join(root, 'backup/admin.json'), ca: join(root, 'backup/ca.crt') } };
  await file(join(root, 'lab.json'), JSON.stringify(result, null, 2));
  console.log(JSON.stringify({ event: 'lab-prepared', configuration: join(root, 'lab.json'), namespaces,
    forgejoInstalled: false, note: 'Protected values are ready; install the retained digest-pinned chart and configure the native OAuth source during qualification. The chart owns SSH NodePort exposure.' }));
}
main().catch(error => { console.error(error.message); process.exitCode = 1; });
