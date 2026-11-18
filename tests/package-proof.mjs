#!/usr/bin/env node
// SPDX-License-Identifier: Apache-2.0
// Invoke only on the authorized Hetzner host. All raw evidence stays there.
// NODE_EXTRA_CA_CERTS=/home/runner/qualification/proxy.crt node tests/package-proof.mjs --lab /home/runner/qualification/lab.json \
//   --chart /srv/retained/candidate/bartl-forgejo-0.2.2.tgz \
//   --artifacts /srv/retained/candidate --work /home/runner/package-proof-01
// Continuation: add --continue-from /absolute/original-work and optionally
// --malformed-from /absolute/malformed-work, always with a fresh --work.
// Both original stopped CRs, their evidenced claims and backup status must still
// exist. An uninstall/reinstall is not that predecessor: use a fresh full run
// after normal removal instead; retain old claims and captures for diagnostics.
// Final-case retry only: --removal-from /absolute/full-run-work requires all 14
// preceding cases passed, removal failed, and the same CR/claims still present
// with successful publication. It never accepts another continuation.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import { mkdir, readFile, writeFile, chmod } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import https from 'node:https';
import { setTimeout as delay } from 'node:timers/promises';

const args = Object.fromEntries(process.argv.slice(2).reduce((pairs, item, i, all) => {
  if (i % 2 === 0) pairs.push([item.replace(/^--/, ''), all[i + 1]]);
  return pairs;
}, []));
let work, lab, chart, artifacts, image, fixtureAccess, installationIdentity, continuation, seq = 0, proxy, backupForward;
const children = new Set(), results = [], snapshots = [], timings = [];
const missingBootstrap = new Set();
const fault = { mode: 'normal', hits: 0, held: new Set() };
const fixtureBytes = Buffer.from('bartl package recovery attachment and package\n');
const lfsBytes = Buffer.from('bartl package large file payload\n'.repeat(4096));
const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const runID = hash(resolve(args.work || '.')).slice(0, 8);
const emit = (event, fields = {}) => console.log(JSON.stringify({ event, ...fields }));
const privateWrite = (path, value) => writeFile(path, value, { mode: 0o600 });
const gitEnv = {
  GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: '/dev/null', GIT_TERMINAL_PROMPT: '0',
  GIT_AUTHOR_NAME: 'Package proof', GIT_AUTHOR_EMAIL: 'package-proof@example.invalid',
  GIT_COMMITTER_NAME: 'Package proof', GIT_COMMITTER_EMAIL: 'package-proof@example.invalid',
};
function environment(extra = {}) {
  return { ...Object.fromEntries(['PATH', 'HOME', 'LANG', 'KUBECONFIG', 'TMPDIR'].filter(k => process.env[k]).map(k => [k, process.env[k]])), ...gitEnv, ...extra };
}
async function command(program, argv, { input, env, cwd = work, allowFailure = false, sensitive = false, quiet = false } = {}) {
  const id = String(++seq).padStart(6, '0');
  const result = await new Promise((done, reject) => {
    const child = spawn(program, argv, { cwd, env: environment(env), stdio: ['pipe', 'pipe', 'pipe'] });
    children.add(child);
    const stdout = [], stderr = [];
    child.stdout.on('data', b => stdout.push(b)); child.stderr.on('data', b => stderr.push(b));
    child.once('error', reject);
    child.once('close', code => { children.delete(child); done({ code, stdout: Buffer.concat(stdout), stderr: Buffer.concat(stderr) }); });
    child.stdin.end(input);
  });
  await privateWrite(join(work, 'raw', `${id}.stdout`), result.stdout);
  await privateWrite(join(work, 'raw', `${id}.stderr`), result.stderr);
  if (!quiet || result.code !== 0) emit('command', { id, program, arguments: sensitive ? '[private]' : argv, exit: result.code });
  if (!allowFailure) assert.equal(result.code, 0, `${program} failed; inspect private raw/${id}.stderr`);
  return { ...result, text: result.stdout.toString().trim() };
}
const invocation = container => [...(container.command || []), ...(container.args || [])];
async function kube(ns, argv, options) {
  const quiet = argv[0] === 'get' && ['pod', 'pods', 'forgejoapplication'].includes(argv[1]);
  return command('kubectl', [...(ns ? ['-n', ns] : []), ...argv], { quiet, ...options });
}
async function object(ns, kind, name = 'forgejo') {
  return JSON.parse((await kube(ns, ['get', kind, name, '-o', 'json'])).text);
}
async function app(slot) { return object(slot.namespace, 'forgejoapplication'); }
function condition(a, name) {
  return !!a.status?.conditions?.some(c => c.type === name && c.status === 'True' && c.observedGeneration === a.metadata.generation);
}
function requireNonfailed(a, label) {
  const failed = a.status?.conditions?.find(c => c.type === 'Failed' && c.status === 'True' && c.observedGeneration === a.metadata.generation);
  if (failed) throw new Error(`${label}: application failed: ${JSON.stringify(a.status)}`);
}
async function until(label, fn, timeout = 1200000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) { const value = await fn(); if (value) return value; await delay(1000); }
  throw new Error(`Timed out: ${label}`);
}
async function observed(slot, name) {
  return until(`${slot.namespace} ${name}`, async () => {
    const a = await app(slot);
    if (a.status?.observedGeneration !== a.metadata.generation) return false;
    if (condition(a, name)) return a;
    if (name !== 'Failed') requireNonfailed(a, `${slot.namespace} ${name}`);
    return false;
  });
}
async function caseRun(name, fn) {
  const started = new Date().toISOString(), start = performance.now();
  try { const evidence = await fn(); results.push({ name, outcome: 'passed', started, elapsedMs: Math.round(performance.now() - start), completed: new Date().toISOString(), evidence }); emit('acceptance', results.at(-1)); }
  catch (error) { results.push({ name, outcome: 'failed', started, reason: error.message }); throw error; }
}
async function patch(slot, spec) {
  await kube(slot.namespace, ['patch', 'forgejoapplication', 'forgejo', '--type=merge', '-p', JSON.stringify({ spec })]);
}
async function patchSecret(namespace, name, data) {
  const file = join(work, 'raw', `secret-patch-${++seq}.json`);
  await privateWrite(file, JSON.stringify({ data }));
  await kube(namespace, ['patch', 'secret', name, '--type=merge', '--patch-file', file], { sensitive: true });
}
async function slotIngress(slot, values) {
  values ||= JSON.parse(await readFile(slot.values, 'utf8'));
  assert.ok(slot.host && slot.url, 'each slot must declare its native host and URL');
  const url = new URL(slot.url);
  assert.equal(url.protocol, 'https:', 'native slot URL must use verified TLS');
  assert.equal(url.host, slot.host, 'slot ingress host must match its native URL');
  assert.equal(url.pathname, '/', 'native slot URL must use the root path');
  const service = values.services[slot.instance || 'forgejo'];
  assert.equal(typeof service.ingress?.enabled, 'boolean', 'slot ingress enabled intent required');
  assert.equal(service.hostname, slot.host, 'slot values must use their native host');
  return { ...service.ingress, host: slot.host };
}
async function start(slot) {
  await patch(slot, { replicas: 1, ingress: await slotIngress(slot) });
  return observed(slot, 'Ready');
}
async function install(slot, version, { backup = true, claim, stagingClaim, identity } = {}) {
  const values = JSON.parse(await readFile(slot.values, 'utf8'));
  identity ||= installationIdentity;
  if (!identity) {
    const declared = { installation: values.installation, instance: slot.instance || 'forgejo' };
    assert.ok(declared.installation && declared.instance, 'installation values must declare both identity components');
    identity = { installation: `${declared.installation}-${runID}`, instance: `${declared.instance}-${runID}` };
  }
  const selected = slot.instance || 'forgejo';
  const service = { ...values.services[selected], version, replicas: 1,
    ...(claim ? { claims: { state: claim, staging: stagingClaim } } : {}) };
  service.backup = backup
    ? { ...service.backup, enabled: true, repository: `rest:https://${lab.proxyHost}:${proxy.address().port}${lab.backup.repositoryPath}` }
    : { enabled: false };
  delete service.backup.endpoint;
  if (service.oidc || service.identityProvider) service.oidc = { ...service.oidc, clientId: service.oidc?.clientId || selected };
  const override = { installation: identity.installation, instance: identity.instance,
    services: { [identity.instance]: service } };
  if (service.identityProvider) {
    const binding = values.resolved?.oidc?.bindings?.[selected];
    assert.ok(binding, 'composed proof slot must retain its selected OIDC binding');
    override.resolved = { oidc: { bindings: { [identity.instance]: { ...binding, clientId: service.oidc.clientId } } } };
  }
  const file = join(work, `${slot.namespace}-${++seq}-values.json`);
  await privateWrite(file, JSON.stringify(override));
  await command('helm', ['upgrade', '--install', 'forgejo', chart, '--namespace', slot.namespace, '--values', slot.values, '--values', file, '--wait=legacy', '--timeout', '5m']);
  return app(slot);
}
async function stop(slot) {
  await patch(slot, { replicas: 0, ingress: { enabled: false } });
  return observed(slot, 'Stopped');
}
async function noWriters(slot, interval = 4000) {
  let initialWorkers;
  const inspect = async (settling = false) => {
    const pods = JSON.parse((await kube(slot.namespace, ['get', 'pods', '-o', 'json'])).text).items;
    // Pod phase can lag actual worker termination. Unknown, waiting and running
    // container states are still writers; a restartable termination is not quiescent.
    const workers = pods.flatMap(p => p.spec.containers.filter(c => c.image === image &&
      invocation(c).some(a => ['serve', 'prepare', 'capture', 'publish'].includes(a))).map(c => {
      const status = p.status.containerStatuses?.find(s => s.name === c.name);
      const terminated = status?.state?.terminated;
      const terminalPod = ['Succeeded', 'Failed'].includes(p.status.phase);
      const mayRestart = !p.metadata.deletionTimestamp &&
        (p.spec.restartPolicy === 'Always' || (!p.spec.restartPolicy) ||
          (p.spec.restartPolicy === 'OnFailure' && terminated?.exitCode !== 0));
      return { key: `${p.metadata.uid}/${c.name}`, name: `${p.metadata.name}/${c.name}`,
        restarts: status?.restartCount || 0, active: !terminalPod && (!terminated || mayRestart) };
    }));
    const active = workers.filter(w => w.active);
    if (!settling) assert.equal(active.length, 0, `active package workers: ${active.map(w => w.name)}`);
    if (initialWorkers) {
      for (const worker of workers) {
        assert.ok(initialWorkers.has(worker.key), `new package worker during quiescence: ${worker.name}`);
        assert.equal(worker.restarts, initialWorkers.get(worker.key), `package worker restarted during quiescence: ${worker.name}`);
      }
    } else initialWorkers = new Map(workers.map(w => [w.key, w.restarts]));
    return active.length === 0;
  };
  // CR failure can precede the kubelet's terminated-container observation.
  // Permit only bounded settling of the workers already present, never a new
  // worker or restart; then enforce the full quiescence stability interval.
  await until(`${slot.namespace} package workers terminate`, () => inspect(true), 30000);
  const deadline = Date.now() + interval;
  while (Date.now() < deadline) { await delay(Math.min(1000, deadline - Date.now())); await inspect(); }
}
async function remove(slot) {
  const before = await app(slot);
  const claims = [before.spec.claim, before.spec.stagingClaim];
  const uids = await Promise.all(claims.map(async name => (await object(slot.namespace, 'pvc', name)).metadata.uid));
  await kube(slot.namespace, ['delete', 'forgejoapplication', 'forgejo', '--wait=true', '--timeout=20m']);
  await noWriters(slot);
  await command('helm', ['uninstall', 'forgejo', '--namespace', slot.namespace, '--wait', '--timeout', '5m']);
  assert.deepEqual(await Promise.all(claims.map(async name => (await object(slot.namespace, 'pvc', name)).metadata.uid)), uids);
  return { claims, uids };
}
async function forward(ns, resource, mapping, kubeconfig) {
  const id = String(++seq).padStart(6, '0');
  const child = spawn('kubectl', [...(kubeconfig ? ['--kubeconfig', kubeconfig] : []), '-n', ns, 'port-forward', resource, ...mapping], { cwd: work, env: environment(), stdio: ['ignore', 'pipe', 'pipe'] });
  children.add(child);
  let output = ''; const chunks = [];
  child.stdout.on('data', b => { output += b.toString(); chunks.push(b); }); child.stderr.on('data', b => chunks.push(b));
  child.once('close', async () => { children.delete(child); await privateWrite(join(work, 'raw', `${id}.port-forward`), Buffer.concat(chunks)); });
  child.once('error', () => {});
  await until(`port-forward ${resource}`, () => { assert.equal(child.exitCode, null, 'port-forward exited'); return mapping.every(m => output.includes(`127.0.0.1:${m.split(':')[0]}`)); }, 30000);
  return child;
}
async function closeChild(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  const closed = new Promise(done => child.once('close', done)); child.kill('SIGTERM'); await closed;
}
function setFault(mode) {
  fault.mode = mode; fault.hits = 0;
  if (mode !== 'hold') { for (const release of fault.held) release(); fault.held.clear(); }
}
async function startProxy() {
  const upstreamPort = lab.backup.forwardPort || 38443;
  backupForward = await forward(lab.backup.namespace, `service/${lab.backup.service}`, [`${upstreamPort}:${lab.backup.port || 8443}`], lab.backup.kubeconfig);
  const ca = await readFile(lab.backup.ca);
  assert.ok(lab.proxyTLS?.cert && lab.proxyTLS?.key, 'fault proxy requires retained TLS inputs');
  proxy = https.createServer({ cert: await readFile(lab.proxyTLS.cert), key: await readFile(lab.proxyTLS.key) }, async (req, res) => {
    const id = `rest-${String(++seq).padStart(6, '0')}`;
    const pathname = new URL(req.url, 'http://fixture.invalid').pathname;
    const selectedData = req.method === 'GET' && /\/(data|snapshots)\/[a-f0-9]+$/.test(pathname);
    if (fault.mode === 'malformed' && req.method === 'GET' && /\/snapshots\/?$/.test(pathname)) {
      fault.hits++; res.writeHead(200, { 'Content-Type': 'application/vnd.x.restic.rest.v2' }); res.end('{malformed lookup');
      await privateWrite(join(work, 'raw', `${id}.json`), JSON.stringify({ method: req.method, path: req.url, injected: 'malformed-lookup' })); return;
    }
    if (fault.mode === 'hold' && selectedData) {
      fault.hits++;
      await new Promise(done => { const release = () => { fault.held.delete(release); done(); }; fault.held.add(release); res.once('close', release); });
      if (res.destroyed) return;
    }
    const upstream = https.request({ hostname: '127.0.0.1', port: upstreamPort, servername: lab.backup.tlsServerName, ca,
      method: req.method, path: req.url, headers: { ...req.headers, host: lab.backup.tlsServerName } }, response => {
      const chunks = [];
      response.on('data', b => chunks.push(b));
      response.on('end', async () => {
        let body = Buffer.concat(chunks); let injected;
        if (fault.mode === 'corrupt' && req.method === 'GET' && /\/data\/[a-f0-9]+$/.test(pathname) && [200, 206].includes(response.statusCode) && body.length) {
          body = Buffer.from(body); body[body.length - 1] ^= 0xff; fault.hits++; injected = 'corrupt-selected-object';
        }
        const headers = { ...response.headers };
        if (injected) { delete headers['transfer-encoding']; headers['content-length'] = body.length; }
        res.writeHead(response.statusCode, headers); res.end(body);
        await privateWrite(join(work, 'raw', `${id}.json`), JSON.stringify({ method: req.method, path: req.url, status: response.statusCode, bytes: body.length, sha256: hash(body), injected }));
      });
    });
    upstream.on('error', async error => { if (!res.headersSent) res.writeHead(502); res.end('fixture upstream unavailable'); await privateWrite(join(work, 'raw', `${id}.error`), error.message); });
    req.pipe(upstream);
  });
  await new Promise(done => proxy.listen(lab.proxyPort || 38080, '0.0.0.0', done));
}
async function endpoint(slot) {
  const a = await app(slot);
  const services = JSON.parse((await kube(slot.namespace, ['get', 'services', '-o', 'json'])).text).items;
  const service = services.find(s => s.metadata.ownerReferences?.some(o => o.uid === a.metadata.uid) && s.spec.ports.some(p => p.port === 3000));
  assert.ok(service, 'public application Service with HTTP port3000 required');
  const port = 33300, sshPort = 33322;
  const child = await forward(slot.namespace, `service/${service.metadata.name}`, [`${port}:3000`, `${sshPort}:${a.spec.ssh?.port || 2222}`]);
  let username, password;
  if (missingBootstrap.has(slot.namespace)) {
    const secrets = JSON.parse((await kube(slot.namespace, ['get', 'secrets', '--field-selector', `metadata.name=${a.spec.bootstrapSecret}`, '-o', 'json'])).text);
    assert.equal(secrets.items.length, 0, 'recovered installation must remain usable without its configured bootstrap Secret');
    assert.ok(fixtureAccess, 'independently retained source administrator credentials required');
    ({ username, password } = fixtureAccess);
  } else {
    const secret = await object(slot.namespace, 'secret', a.spec.bootstrapSecret);
    username = Buffer.from(secret.data.username, 'base64').toString(); password = Buffer.from(secret.data.password, 'base64').toString();
  }
  return { slot, child, base: `http://127.0.0.1:${port}`, port, sshPort, username, password,
    basic: `Basic ${Buffer.from(`${username}:${password}`).toString('base64')}` };
}
async function request(e, path, { method = 'GET', body, auth = e.basic, headers = {}, expected = [200], raw = false } = {}) {
  if (body !== undefined && !(body instanceof FormData) && !Buffer.isBuffer(body)) { headers = { ...headers, 'Content-Type': 'application/json' }; body = JSON.stringify(body); }
  const response = await fetch(`${e.base}${path}`, { method, headers: { ...(auth ? { Authorization: auth } : {}), ...headers }, body, redirect: 'manual', signal: AbortSignal.timeout(30000) });
  const bytes = Buffer.from(await response.arrayBuffer()), id = `http-${String(++seq).padStart(6, '0')}`;
  await privateWrite(join(work, 'raw', `${id}.body`), bytes);
  emit('http', { id, method, path, status: response.status, sha256: hash(bytes) });
  assert.ok(expected.includes(response.status), `${method} ${path}: ${response.status}; see ${id}`);
  return raw ? bytes : bytes.length ? JSON.parse(bytes) : null;
}
async function hostIdentity(e, expected) {
  const scan = (await command('ssh-keyscan', ['-p', String(e.sshPort), '-t', 'rsa,ecdsa,ed25519', '127.0.0.1'])).text;
  const lines = scan.split('\n').filter(line => line.trim() && !line.startsWith('#'));
  const keys = [...new Set(lines.map(line => line.trim().split(/\s+/).slice(1).join(' ')))].sort();
  assert.ok(keys.length > 0, 'native server must offer an SSH host key');
  for (const key of keys) assert.match(key, /^(ssh-rsa|ecdsa-sha2-\S+|ssh-ed25519) [A-Za-z0-9+/]+=*$/, 'valid offered SSH public host key');
  if (expected) assert.deepEqual(keys, expected, 'native SSH host keyset changed');
  await privateWrite(join(work, 'known_hosts'), `${[...new Set(lines)].sort().join('\n')}\n`);
  return keys;
}
const repoPath = e => `/api/v1/repos/${e.username}/proof`;
async function transports(e, stage, create = false) {
  const client = join(work, `http-${stage}`), sshClient = join(work, `ssh-${stage}`);
  const authEnv = { GIT_CONFIG_COUNT: '2', GIT_CONFIG_KEY_0: 'http.extraHeader', GIT_CONFIG_VALUE_0: `Authorization: ${e.basic}`,
    GIT_CONFIG_KEY_1: 'lfs.url', GIT_CONFIG_VALUE_1: `${e.base}/${e.username}/proof.git/info/lfs` };
  await command('git', ['clone', `${e.base}/${e.username}/proof.git`, client], { env: { ...authEnv, GIT_LFS_SKIP_SMUDGE: '1' }, sensitive: true });
  await command('git', ['lfs', 'install', '--local'], { cwd: client });
  if (create) {
    await command('git', ['checkout', '-b', 'main'], { cwd: client });
    await command('git', ['lfs', 'track', '*.bin'], { cwd: client });
    await privateWrite(join(client, 'payload.bin'), lfsBytes);
    await privateWrite(join(client, 'README.md'), 'Package qualification fixture\n');
  } else {
    await command('git', ['lfs', 'pull'], { cwd: client, env: authEnv, sensitive: true });
    assert.deepEqual(await readFile(join(client, 'payload.bin')), lfsBytes);
    assert.equal(await readFile(join(client, 'README.md'), 'utf8'), 'Package qualification fixture\n');
    const ancestry = await command('git', ['merge-base', '--is-ancestor', await readFile(join(work, 'initial-head'), 'utf8'), 'HEAD'], { cwd: client });
    assert.equal(ancestry.code, 0, 'original Git history retained');
  }
  await privateWrite(join(client, `${stage}.txt`), `${stage} HTTP write\n`);
  await command('git', ['add', '.'], { cwd: client }); await command('git', ['commit', '-m', `Exercise ${stage}`], { cwd: client });
  await command('git', ['push', '-u', 'origin', 'main'], { cwd: client, env: authEnv, sensitive: true });
  const sshEnv = { GIT_SSH_COMMAND: `ssh -i ${join(work, 'client-key')} -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=${join(work, 'known_hosts')}`, GIT_LFS_SKIP_SMUDGE: '1' };
  await command('git', ['clone', `ssh://git@127.0.0.1:${e.sshPort}/${e.username}/proof.git`, sshClient], { env: sshEnv });
  await privateWrite(join(sshClient, `ssh-${stage}.txt`), `${stage} SSH write\n`);
  await command('git', ['add', '.'], { cwd: sshClient }); await command('git', ['commit', '-m', `Exercise SSH ${stage}`], { cwd: sshClient });
  await command('git', ['push', 'origin', 'main'], { cwd: sshClient, env: sshEnv });
  if (create) await privateWrite(join(work, 'initial-head'), (await command('git', ['rev-parse', 'HEAD'], { cwd: sshClient })).text);
}
async function denied(e, stage) {
  await request(e, repoPath(e), { auth: null, expected: [401, 403, 404] });
  await request(e, repoPath(e), { auth: `Basic ${Buffer.from(`proof-outsider:${e.password}`).toString('base64')}`, expected: [403, 404] });
  const result = await command('git', ['ls-remote', `${e.base}/${e.username}/proof.git`], { allowFailure: true });
  assert.notEqual(result.code, 0, 'anonymous private Git access must fail');
  const sshEnv = { GIT_SSH_COMMAND: `ssh -i ${join(work, 'denied-key')} -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=${join(work, 'known_hosts')}` };
  const ssh = await command('git', ['ls-remote', `ssh://git@127.0.0.1:${e.sshPort}/${e.username}/proof.git`], { env: sshEnv, allowFailure: true });
  assert.notEqual(ssh.code, 0, `${stage} unregistered SSH identity must fail`);
}
async function configureOIDC(e) {
  assert.ok(lab.oidc?.configuration, 'protected OIDC configuration path required');
  const oidc = JSON.parse(await readFile(lab.oidc.configuration, 'utf8'));
  for (const key of ['providerName', 'clientId', 'clientSecret', 'discoveryURL']) assert.ok(oidc[key], `missing OIDC ${key}`);
  const pods = JSON.parse((await kube(e.slot.namespace, ['get', 'pods', '-o', 'json'])).text).items;
  const serving = pods.find(p => !p.metadata.deletionTimestamp && p.spec.containers.some(c => invocation(c).includes('serve')));
  assert.ok(serving, 'native serving pod required for supported administration CLI');
  const quote = value => `'${String(value).replaceAll("'", "'\\''")}'`;
  const argv = ['/usr/local/lib/forgejo/16.0.7/forgejo', '--work-path', '/data', '--custom-path', '/data/custom', '--config', '/data/app.ini',
    'admin', 'auth', 'add-oauth', '--name', oidc.providerName, '--provider', 'openidConnect', '--key', oidc.clientId,
    '--secret', oidc.clientSecret, '--auto-discover-url', oidc.discoveryURL, '--scopes', 'openid', '--scopes', 'profile', '--scopes', 'email'];
  // Secret travels only over protected kubectl stdin, never argv or emitted logs.
  await kube(e.slot.namespace, ['exec', '-i', serving.metadata.name, '--', '/bin/sh', '-s'], {
    input: `umask 077\nexec env HOME=/data/home USER=git LOGNAME=git ${argv.map(quote).join(' ')}\n`, sensitive: true,
  });
  await privateWrite(join(work, 'oidc-source.json'), JSON.stringify({ providerName: oidc.providerName, clientId: oidc.clientId, discoveryURL: oidc.discoveryURL, callbackURL: oidc.callbackURL }));
}
async function seedPullRequest(e) {
  const cwd = join(work, 'http-source');
  const env = { GIT_CONFIG_COUNT: '1', GIT_CONFIG_KEY_0: 'http.extraHeader', GIT_CONFIG_VALUE_0: `Authorization: ${e.basic}` };
  await command('git', ['fetch', 'origin', 'main'], { cwd, env, sensitive: true });
  await command('git', ['checkout', '-b', 'proof-change', 'origin/main'], { cwd });
  await privateWrite(join(cwd, 'review.txt'), 'A useful change awaiting review after recovery.\n');
  await command('git', ['add', 'review.txt'], { cwd });
  await command('git', ['commit', '-m', 'Propose a reviewable change'], { cwd });
  await command('git', ['push', 'origin', 'proof-change'], { cwd, env, sensitive: true });
  const pull = await request(e, `${repoPath(e)}/pulls`, { method: 'POST', expected: [201], body: { base: 'main', head: 'proof-change', title: 'Preserved pull request', body: 'Review must remain possible after recovery' } });
  await privateWrite(join(work, 'pull-request.json'), JSON.stringify({ number: pull.number, head: pull.head.sha }));
}
async function seed(e) {
  fixtureAccess = { username: e.username, password: e.password };
  await privateWrite(join(work, 'fixture-access.json'), JSON.stringify(fixtureAccess));
  await request(e, '/api/v1/admin/users', { method: 'POST', expected: [201], body: { username: 'proof-outsider', email: 'proof-outsider@example.invalid', password: e.password, must_change_password: false } });
  await request(e, '/api/v1/user/keys', { method: 'POST', expected: [201], body: { title: 'package-proof', key: await readFile(join(work, 'client-key.pub'), 'utf8') } });
  await request(e, '/api/v1/user/repos', { method: 'POST', expected: [201], body: { name: 'proof', private: true, default_branch: 'main', auto_init: false } });
  await transports(e, 'source', true);
  await until('native post-receive repository update', async () =>
    (await request(e, repoPath(e))).empty === false, 30000);
  await request(e, `${repoPath(e)}/issues`, { method: 'POST', expected: [201], body: { title: 'Preserved issue', body: 'Package recovery fixture' } });
  await request(e, `${repoPath(e)}/issues/1/comments`, { method: 'POST', expected: [201], body: { body: 'Preserved comment' } });
  const form = new FormData(); form.append('attachment', new Blob([fixtureBytes], { type: 'text/plain' }), 'proof.txt');
  const attachment = await request(e, `${repoPath(e)}/issues/1/assets`, { method: 'POST', expected: [201], body: form });
  await privateWrite(join(work, 'attachment.json'), JSON.stringify(attachment));
  await request(e, `/api/packages/${e.username}/generic/package-proof/1.0.0/proof.txt`, { method: 'PUT', expected: [201], body: fixtureBytes, headers: { 'Content-Type': 'application/octet-stream' } });
  await request(e, `${repoPath(e)}/releases`, { method: 'POST', expected: [201], body: { tag_name: 'v1.0.0', target_commitish: 'main', name: 'Preserved release' } });
  await seedPullRequest(e);
  await denied(e, 'source');
}
async function verify(e, stage, version) {
  assert.ok((await request(e, '/api/v1/version')).version.startsWith(version));
  assert.equal((await request(e, '/api/v1/users/proof-outsider')).login, 'proof-outsider');
  assert.equal((await request(e, `${repoPath(e)}/issues/1`)).title, 'Preserved issue');
  assert.equal((await request(e, `${repoPath(e)}/issues/1/comments`))[0].body, 'Preserved comment');
  const attachment = JSON.parse(await readFile(join(work, 'attachment.json'), 'utf8'));
  assert.ok((await request(e, `${repoPath(e)}/issues/1/assets`)).some(x => x.uuid === attachment.uuid));
  assert.deepEqual(await request(e, new URL(attachment.browser_download_url).pathname, { raw: true }), fixtureBytes);
  assert.deepEqual(await request(e, `/api/packages/${e.username}/generic/package-proof/1.0.0/proof.txt`, { raw: true }), fixtureBytes);
  assert.equal((await request(e, `${repoPath(e)}/releases/tags/v1.0.0`)).name, 'Preserved release');
  const pull = JSON.parse(await readFile(join(work, 'pull-request.json'), 'utf8'));
  const recoveredPull = await request(e, `${repoPath(e)}/pulls/${pull.number}`);
  assert.equal(recoveredPull.title, 'Preserved pull request'); assert.equal(recoveredPull.state, 'open'); assert.equal(recoveredPull.head.sha, pull.head);
  assert.ok((await request(e, `${repoPath(e)}/pulls/${pull.number}/files`)).some(file => file.filename === 'review.txt'));
  const oidc = JSON.parse(await readFile(join(work, 'oidc-source.json'), 'utf8'));
  const loginPage = (await request(e, '/user/login', { auth: null, raw: true })).toString();
  assert.ok(loginPage.includes(`/user/oauth2/${encodeURIComponent(oidc.providerName)}`), 'preserved native OIDC login source');
  await transports(e, stage); await denied(e, stage);
  await request(e, `${repoPath(e)}/issues`, { method: 'POST', expected: [201], body: { title: `${stage} new work` } });
  await request(e, `/api/packages/${e.username}/generic/package-proof/${stage}/proof.txt`, { method: 'PUT', expected: [201], body: fixtureBytes, headers: { 'Content-Type': 'application/octet-stream' } });
}
async function backup(slot, { success = true, request } = {}) {
  request ||= `proof-${Date.now()}-${randomBytes(4).toString('hex')}`;
  const started = performance.now(); let unavailableAt, availableAt, previousAvailableAt = started, maximumSampleGapMs = 0, previousSample = started;
  async function availability() {
    const pods = JSON.parse((await kube(slot.namespace, ['get', 'pods', '-o', 'json'])).text).items;
    const now = performance.now();
    maximumSampleGapMs = Math.max(maximumSampleGapMs, now - previousSample); previousSample = now;
    const ready = pods.some(p => !p.metadata.deletionTimestamp && p.spec.containers.some(c => invocation(c).includes('serve')) && p.status.conditions?.some(c => c.type === 'Ready' && c.status === 'True'));
    if (!ready && unavailableAt === undefined) unavailableAt = now;
    if (ready && unavailableAt === undefined) previousAvailableAt = now;
    if (ready && unavailableAt !== undefined && availableAt === undefined) availableAt = now;
    return ready;
  }
  await kube(slot.namespace, ['annotate', 'forgejoapplication', 'forgejo', `forgejo.bartl.sh/backup-request=${request}`, '--overwrite']);
  const a = await until(`backup ${request}`, async () => {
    await availability(); const a = await app(slot); requireNonfailed(a, `backup ${request}`);
    return a.status?.backup?.request === request && a;
  });
  await until('service Ready following capture/publication', async () => {
    requireNonfailed(await app(slot), 'service Ready following capture/publication');
    return availability();
  });
  timings.push({ kind: 'backup', namespace: slot.namespace, request, totalMs: Math.round(performance.now() - started),
    observedServiceInterruptionMs: unavailableAt === undefined ? null : Math.round(availableAt - unavailableAt),
    interruptionUpperBoundMs: unavailableAt === undefined ? null : Math.round(availableAt - previousAvailableAt),
    maximumSampleGapMs: Math.round(maximumSampleGapMs), basis: 'actual serving pod Ready observations; polling bounds, fixture scale only' });
  emit('timing', timings.at(-1));
  assert.equal(a.status.backup.success, success, JSON.stringify(a.status));
  if (success) { assert.match(a.status.backup.snapshot, /^[a-f0-9]{64}$/); snapshots.push({ namespace: slot.namespace, ...a.status.backup }); }
  return a;
}
async function freshClaims(slot, suffix) {
  const names = [`proof-${runID}-${suffix}-state`, `proof-${runID}-${suffix}-capture`];
  for (const name of names) {
    await kube(slot.namespace, ['create', '-f', '-'], { input: JSON.stringify({ apiVersion: 'v1', kind: 'PersistentVolumeClaim', metadata: { name }, spec: { accessModes: ['ReadWriteOnce'], resources: { requests: { storage: '2Gi' } } } }) });
  }
  return { claim: names[0], stagingClaim: names[1] };
}
async function readOnlyStateDigest(slot, claim) {
  const name = `proof-inspect-${runID}-${++seq}`;
  await kube(slot.namespace, ['create', '-f', '-'], { input: JSON.stringify({
    apiVersion: 'v1', kind: 'Pod', metadata: { name },
    spec: {
      restartPolicy: 'Never', automountServiceAccountToken: false,
      securityContext: { runAsUser: 1000, runAsGroup: 1000, runAsNonRoot: true },
      containers: [{ name: 'inspect', image, command: ['/bin/sh', '-ec', 'cd /state; find . -type f -exec sha256sum {} + | sort'],
        securityContext: { allowPrivilegeEscalation: false, capabilities: { drop: ['ALL'] } },
        volumeMounts: [{ name: 'state', mountPath: '/state', readOnly: true }] }],
      volumes: [{ name: 'state', persistentVolumeClaim: { claimName: claim, readOnly: true } }],
    },
  }) });
  try {
    await until('read-only state inspection completes', async () => {
      const pod = await object(slot.namespace, 'pod', name);
      assert.notEqual(pod.status.phase, 'Failed', 'read-only state inspection failed');
      return pod.status.phase === 'Succeeded';
    }, 120000);
    const inventory = (await kube(slot.namespace, ['logs', name])).text;
    assert.match(inventory, /[a-f0-9]{64}  \.\/data\/forgejo\.db(?:\n|$)/, 'native SQLite file included in complete content inventory');
    return hash(inventory);
  } finally { await kube(slot.namespace, ['delete', 'pod', name, '--wait=true', '--timeout=2m']); }
}
async function recoveryInputDigest(slot) {
  const a = await app(slot), secret = await object(slot.namespace, 'secret', a.spec.backup.readerSecret);
  const decode = key => Buffer.from(secret.data[key], 'base64').toString();
  const backupEndpoint = { base: `https://${lab.proxyHost}:${proxy.address().port}`, basic: `Basic ${Buffer.from(`${decode('username')}:${decode('restPassword')}`).toString('base64')}` };
  const point = await request(backupEndpoint, `${lab.backup.repositoryPath}snapshots/${a.status.backup.snapshot}`, { raw: true });
  return { sourceState: await readOnlyStateDigest(slot, a.spec.claim), snapshot: a.status.backup.snapshot, snapshotSHA256: hash(point),
    configurationSHA256: hash(JSON.stringify((await object(slot.namespace, 'secret', a.spec.configurationSecret)).data)) };
}
async function failedTarget(slot, name, mode) {
  const claims = await freshClaims(slot, name); setFault(mode);
  await install(slot, '17.0.1', { ...claims, identity: (await app(lab.slots.blue)).spec.identity });
  if (mode === 'hold') {
    await until('restore reaches real selected repository object', async () => {
      requireNonfailed(await app(slot), 'restore reaches real selected repository object');
      return fault.hits > 0;
    });
    const a = await app(slot);
    assert.equal(a.status.operation.kind, 'prepare');
    const jobs = JSON.parse((await kube(slot.namespace, ['get', 'jobs', '-o', 'json'])).text).items;
    const job = jobs.find(j => j.metadata.ownerReferences?.some(o => o.uid === a.metadata.uid) && (j.spec.template.spec.containers || []).some(c => invocation(c).includes('prepare')));
    assert.ok(job, 'public active prepare Job required');
    const pods = JSON.parse((await kube(slot.namespace, ['get', 'pods', '-l', `job-name=${job.metadata.name}`, '-o', 'json'])).text).items;
    assert.ok(pods.length, 'active prepare pod required');
    // Foreground deletion confirms termination and prevents Kubernetes replaying
    // an intentionally deleted pod as another attempt of the same Job.
    await kube(slot.namespace, ['delete', 'job', job.metadata.name, '--cascade=foreground', '--wait=true', '--timeout=3m']);
    setFault('normal');
  }
  const a = await until(`${slot.namespace} ${mode} fault fails closed`, async () => {
    const target = await app(slot);
    assert.equal(condition(target, 'Ready'), false,
      `${slot.namespace}: unexpected current-generation Ready during ${mode} fault: ${JSON.stringify({
        generation: target.metadata.generation, faultHits: fault.hits, status: target.status,
      })}`);
    if (target.status?.observedGeneration !== target.metadata.generation) return false;
    return condition(target, 'Failed') ? target : false;
  });
  assert.ok(fault.hits > 0 || mode === 'hold', 'fault must have exercised a real repository response');
  setFault('normal'); await noWriters(slot);
  // Reconcile the same public intent once more; it must not silently initialize.
  await kube(slot.namespace, ['annotate', 'forgejoapplication', 'forgejo', `proof.bartl.sh/reobserve=${Date.now()}`, '--overwrite']);
  await delay(4000); assert.ok(condition(await app(slot), 'Failed')); await noWriters(slot);
  await remove(slot);
  return { claims, operation: a.status.operation, fault: mode };
}
async function publishedCapture(slot, predecessor, operation) {
  return until('identity-bound succeeded publication Job', async () => {
    const jobs = JSON.parse((await kube(slot.namespace, ['get', 'jobs', '-o', 'json'])).text).items;
    const pods = JSON.parse((await kube(slot.namespace, ['get', 'pods', '-o', 'json'])).text).items;
    for (const job of jobs) {
      if (!job.status?.succeeded || !job.metadata.ownerReferences?.some(o => o.controller && o.uid === predecessor.metadata.uid)) continue;
      for (const pod of pods.filter(p => p.metadata.ownerReferences?.some(o => o.controller && o.uid === job.metadata.uid))) {
        if (!pod.spec.containers.some(c => c.name === 'forgejo' && c.image === image && invocation(c).includes('publish'))) continue;
        const terminated = pod.status?.containerStatuses?.find(c => c.name === 'forgejo')?.state?.terminated;
        if (!terminated || terminated.exitCode !== 0) continue;
        let result; try { result = JSON.parse(terminated.message); } catch { continue; }
        if (result.operation !== operation.id || result.command !== 'publish' || !result.success) continue;
        assert.equal(result.installation, predecessor.spec.identity.installation);
        assert.equal(result.instance, predecessor.spec.identity.instance);
        assert.equal(result.version, operation.version);
        assert.match(result.snapshot, /^[a-f0-9]{64}$/);
        return { job: job.metadata.name, jobUID: job.metadata.uid, pod: pod.metadata.name, podUID: pod.metadata.uid, result };
      }
    }
    return false;
  }, 60000);
}
async function diagnostics() {
  if (!lab?.slots) return;
  for (const slot of Object.values(lab.slots)) {
    try {
      await kube(slot.namespace, ['get', 'forgejoapplication,pods,jobs,deployments,services,pvc', '-o', 'yaml'], { allowFailure: true });
      const pods = JSON.parse((await kube(slot.namespace, ['get', 'pods', '-o', 'json'], { allowFailure: true })).text || '{"items":[]}').items;
      for (const p of pods) for (const c of p.spec.containers) await kube(slot.namespace, ['logs', p.metadata.name, '-c', c.name, '--timestamps=true'], { allowFailure: true });
    } catch (error) { emit('diagnostics-error', { namespace: slot.namespace, reason: error.message }); }
  }
}
async function main() {
  assert.equal(process.platform, 'linux', 'remote Linux only'); assert.equal(process.arch, 'x64');
  assert.equal(process.env.BARTL_FORGEJO_LAB, 'hetzner', 'explicit authorized lab opt-in required');
  assert.ok(Number(process.versions.node.split('.')[0]) >= 22);
  for (const key of ['work', 'lab', 'chart', 'artifacts']) assert.ok(args[key]?.startsWith('/'), `--${key} absolute path required`);
  const requestedWork = resolve(args.work); assert.ok(!/\s/.test(requestedWork));
  await mkdir(requestedWork, { mode: 0o700 }); work = requestedWork;
  await chmod(work, 0o700); await mkdir(join(work, 'raw'), { mode: 0o700 });
  lab = JSON.parse(await readFile(args.lab, 'utf8')); chart = args.chart; artifacts = args.artifacts;
  image = (await readFile(join(artifacts, 'image-reference'), 'utf8')).trim(); assert.match(image, /@sha256:[a-f0-9]{64}$/);
  const imageDigest = (await readFile(join(artifacts, 'image.digest'), 'utf8')).trim(); assert.ok(image.endsWith(`@${imageDigest}`));
  const sourceTree = (await readFile(join(artifacts, 'source-tree'), 'utf8')).trim(); assert.match(sourceTree, /^[a-f0-9]{40}$/);
  const baseRevision = (await readFile(join(artifacts, 'base-revision'), 'utf8')).trim(); assert.match(baseRevision, /^[a-f0-9]{40}$/);
  const identity = { image, imageDigest, sourceTree, baseRevision, chart, chartSHA256: hash(await readFile(chart)), sourceArchiveSHA256: hash(await readFile(join(artifacts, 'source.tar.gz'))), proofSHA256: hash(await readFile(new URL(import.meta.url))) };
  let hostKey;
  // Continuation reuses evidenced happy cases for these exact artifacts.
  // --malformed-from may additionally retain the first continuation's malformed
  // proof; all remaining fault and retained-state reinstall cases execute here.
  assert.ok(!args['removal-from'] || (!args['continue-from'] && !args['malformed-from']),
    '--removal-from cannot be combined with other continuation modes');
  if (args['continue-from'] || args['removal-from']) {
    const previous = args['removal-from'] || args['continue-from'];
    assert.ok(previous.startsWith('/'), 'absolute prior workdir required');
    const priorWork = resolve(previous);
    assert.notEqual(priorWork, work, 'continuation requires a fresh workdir');
    const priorResultPath = join(priorWork, 'result.json'), priorBytes = await readFile(priorResultPath);
    const prior = JSON.parse(priorBytes);
    assert.equal(prior.workdir, priorWork, 'prior result must identify its workdir');
    assert.ok(!prior.continuation, 'continuation requires an original full-run result');
    assert.match(prior.identity?.proofSHA256 || '', /^[a-f0-9]{64}$/, 'prior proof identity required');
    for (const key of ['image', 'imageDigest', 'sourceTree', 'baseRevision', 'chartSHA256', 'sourceArchiveSHA256']) {
      assert.equal(prior.identity?.[key], identity[key], `continuation artifact mismatch: ${key}`);
    }
    const required = [
      'install-without-backup', 'configured-empty-repository', 'native-old-source-fixture',
      'existing-restart-backup-inaccessible', 'capture-and-stop-source',
      'native-old-to-new-independent-pvc', 'publication-failure-resume-retained-capture',
      'same-version-replacement-new-backup', 'same-version-blue-to-green-independent-pvc',
      'native-downgrade-rejected-without-state-change',
    ];
    if (args['removal-from']) {
      required.push('malformed-lookup-fails-closed', 'corrupt-selected-point-no-fallback',
        'interrupted-mutation-remains-stopped', 'stop-overrides-operation-completion');
      assert.equal(prior.outcome, 'failed');
      assert.equal(prior.results?.length, 15, 'removal retry requires exactly the original full suite');
      assert.deepEqual(prior.results.slice(0, 14).map(r => r.name), required);
      assert.equal(prior.results[14].name, 'removal-retained-state-reinstall');
      assert.equal(prior.results[14].outcome, 'failed');
      const reason = prior.results[14].reason;
      assert.ok(reason?.startsWith('publication must preserve the original capture operation') ||
        reason === 'original capture requires a retained succeeded publication Job and identity-bound termination result',
        'only the superseded publication assertion or Job-observation race may be continued');
    }
    const passed = required.map(name => {
      const matches = prior.results?.filter(result => result.name === name) || [];
      assert.equal(matches.length, 1, `prior result must contain exactly one ${name}`);
      assert.equal(matches[0].outcome, 'passed', `prior ${name} must have passed`);
      return matches[0];
    });
    hostKey = passed.find(result => result.name === 'native-old-source-fixture').evidence.hostKey;
    assert.ok(Array.isArray(hostKey) && hostKey.length, 'prior native SSH host keyset required');
    const fixtureFiles = ['fixture-access.json', 'initial-head', 'attachment.json', 'pull-request.json',
      'oidc-source.json', 'client-key', 'client-key.pub', 'denied-key', 'denied-key.pub'];
    const files = [];
    for (const name of fixtureFiles) {
      const bytes = await readFile(join(priorWork, name));
      assert.ok(bytes.length > 0 && bytes.length <= 65536, `bounded fixture file required: ${name}`);
      await privateWrite(join(work, name), bytes);
      files.push({ name, sha256: hash(bytes) });
    }
    fixtureAccess = JSON.parse(await readFile(join(work, 'fixture-access.json'), 'utf8'));
    assert.ok(fixtureAccess.username && fixtureAccess.password, 'prior fixture credentials required');
    continuation = { priorResultPath, priorResultSHA256: hash(priorBytes), priorIdentity: prior.identity,
      priorOutcome: prior.outcome, priorFailedCases: prior.results.filter(result => result.outcome === 'failed'),
      passedEvidence: passed, fixtureFiles: files };
    if (args['removal-from']) continuation.removalOnly = true;
    if (args['malformed-from']) {
      assert.ok(args['malformed-from'].startsWith('/'), '--malformed-from absolute workdir required');
      const malformedWork = resolve(args['malformed-from']);
      assert.ok(malformedWork !== work && malformedWork !== priorWork, 'malformed evidence requires a separate workdir');
      const resultPath = join(malformedWork, 'result.json'), bytes = await readFile(resultPath);
      const report = JSON.parse(bytes);
      assert.equal(report.workdir, malformedWork, 'malformed result must identify its workdir');
      for (const key of ['image', 'imageDigest', 'sourceTree', 'baseRevision', 'chartSHA256', 'sourceArchiveSHA256']) {
        assert.equal(report.identity?.[key], identity[key], `malformed evidence artifact mismatch: ${key}`);
      }
      assert.match(report.identity?.proofSHA256 || '', /^[a-f0-9]{64}$/, 'malformed proof identity required');
      assert.equal(report.continuation?.priorResultPath, priorResultPath, 'malformed continuation must reference the original run');
      assert.equal(report.continuation?.priorResultSHA256, hash(priorBytes), 'malformed continuation must bind the original result bytes');
      assert.deepEqual(report.continuation?.priorIdentity, prior.identity);
      assert.deepEqual(report.continuation?.passedEvidence, passed);
      assert.ok(!report.continuation?.malformedEvidence, 'malformed evidence cannot be chained');
      assert.equal(report.results?.length, 2, 'malformed continuation must stop at the corrupt case');
      assert.equal(report.results[0].name, 'malformed-lookup-fails-closed');
      assert.equal(report.results[0].outcome, 'passed', 'malformed lookup must have passed');
      assert.equal(report.results[0].evidence?.fault, 'malformed');
      assert.equal(report.results[1].name, 'corrupt-selected-point-no-fallback');
      assert.equal(report.results[1].outcome, 'failed', 'only failed corruption may be retried');
      assert.equal(report.outcome, 'failed');
      continuation.malformedEvidence = { resultPath, resultSHA256: hash(bytes), identity: report.identity,
        passed: report.results[0], failed: report.results[1] };
    }
  }
  assert.ok(!args['malformed-from'] || continuation, '--malformed-from requires --continue-from');
  await privateWrite(join(work, 'artifact-identity.json'), JSON.stringify(identity, null, 2));
  await command('sha256sum', ['--check', 'SHA256SUMS'], { cwd: artifacts });
  for (const tool of ['kubectl', 'helm', 'git', 'ssh', 'ssh-keygen', 'ssh-keyscan']) await command(tool, tool === 'git' ? ['lfs', 'version'] : tool === 'kubectl' ? ['version', '--client=true', '-o', 'json'] : tool === 'helm' ? ['version'] : ['-V'], { allowFailure: ['ssh-keygen', 'ssh-keyscan'].includes(tool) });
  if (!continuation) {
    await command('ssh-keygen', ['-q', '-t', 'ed25519', '-N', '', '-f', join(work, 'client-key')]);
    await command('ssh-keygen', ['-q', '-t', 'ed25519', '-N', '', '-f', join(work, 'denied-key')]);
  }
  assert.equal(new Set(Object.values(lab.slots).map(s => s.namespace)).size, 3, 'three independent namespace slots required');
  const { blue, green, empty } = lab.slots;
  if (continuation && !continuation.removalOnly) {
    const source = await app(blue), target = await app(green);
    assert.deepEqual(source.spec.identity, target.spec.identity, 'continued slots must retain one logical installation');
    const priorSuffix = hash(resolve(args['continue-from'])).slice(0, 8);
    assert.notEqual(runID, priorSuffix, 'continuation requires a distinct fresh-PVC run suffix');
    for (const [slot, a, caseName] of [
      [blue, source, 'same-version-replacement-new-backup'],
      [green, target, 'same-version-blue-to-green-independent-pvc'],
    ]) {
      const values = JSON.parse(await readFile(slot.values, 'utf8'));
      const declared = { installation: values.installation, instance: slot.instance || 'forgejo' };
      assert.deepEqual(a.spec.identity, { installation: `${declared.installation}-${priorSuffix}`,
        instance: `${declared.instance}-${priorSuffix}` }, 'live identity must belong to prior qualification');
      const evidence = continuation.passedEvidence.find(result => result.name === caseName).evidence;
      assert.equal(a.spec.claim, evidence.claims.claim, 'continued state claim must match prior evidence');
      assert.equal(a.spec.stagingClaim, evidence.claims.stagingClaim, 'continued staging claim must match prior evidence');
      assert.equal(a.status?.backup?.snapshot, evidence.newPoint.snapshot, 'continued backup must match prior evidence');
      assert.equal(a.spec.replicas, 0, 'continued slots must already be stopped');
      assert.ok(condition(a, 'Stopped') && a.status?.observedGeneration === a.metadata.generation,
        'continued slots must have observed their stopped intent');
      await noWriters(slot);
    }
    installationIdentity = source.spec.identity;
    missingBootstrap.add(green.namespace);
  }
  if (continuation?.removalOnly) {
    const target = await app(green), source = await app(blue);
    const suffix = hash(resolve(args['removal-from'])).slice(0, 8);
    assert.notEqual(runID, suffix, 'removal retry requires a fresh workdir');
    const values = JSON.parse(await readFile(green.values, 'utf8'));
    const declared = { installation: values.installation, instance: green.instance || 'forgejo' };
    assert.deepEqual(target.spec.identity, { installation: `${declared.installation}-${suffix}`,
      instance: `${declared.instance}-${suffix}` });
    assert.deepEqual(source.spec.identity, target.spec.identity);
    const retained = continuation.passedEvidence.find(r => r.name === 'same-version-blue-to-green-independent-pvc').evidence;
    assert.equal(target.spec.claim, retained.claims.claim);
    assert.equal(target.spec.stagingClaim, retained.claims.stagingClaim);
    assert.equal(target.spec.version, '17.0.1');
    assert.equal(target.status?.backup?.success, true);
    assert.equal(target.status?.operation?.kind, 'publish');
    assert.equal(target.status?.operation?.phase, 'Succeeded');
    assert.equal(target.status.backup.operation, target.status.operation.id);
    assert.match(target.status.backup.snapshot, /^[a-f0-9]{64}$/);
    const operation = continuation.passedEvidence.find(r => r.name === 'stop-overrides-operation-completion').evidence.status.operation;
    assert.equal(operation.claim, target.spec.claim);
    assert.equal(operation.stagingClaim, target.spec.stagingClaim);
    assert.equal(operation.version, target.spec.version);
    // The old successful Job is controlled by this CR UID, proving it was not
    // replaced after the prior run. Its native result binds the original capture.
    const publication = await publishedCapture(green, target, operation);
    continuation.removalPredecessor = { uid: target.metadata.uid, operation, publication, backup: target.status.backup };
    installationIdentity = target.spec.identity;
    missingBootstrap.add(green.namespace);
    assert.equal(source.spec.replicas, 0);
    await observed(blue, 'Stopped'); await noWriters(blue);
  }
  // Drain existing declared installations through their finalizers. Keep every
  // old claim and its diagnostics; each new run receives independent claims.
  for (const slot of continuation ? [empty] : [blue, green, empty]) {
    const existing = await kube(slot.namespace, ['get', 'forgejoapplication', 'forgejo', '--ignore-not-found', '-o', 'json']);
    if (existing.text) await remove(slot);
    else {
      const releases = JSON.parse((await command('helm', ['list', '--namespace', slot.namespace, '--filter', '^forgejo$', '--output', 'json'])).text);
      if (releases.length) await command('helm', ['uninstall', 'forgejo', '--namespace', slot.namespace, '--wait', '--timeout', '5m']);
      await noWriters(slot);
    }
  }
  await startProxy();
  if (!continuation) {
  await caseRun('install-without-backup', async () => { const claims = await freshClaims(empty, 'no-backup'); await install(empty, '17.0.1', { ...claims, backup: false }); await observed(empty, 'Ready'); const e = await endpoint(empty); try { assert.ok((await request(e, '/api/v1/version')).version.startsWith('17.0.1')); } finally { await closeChild(e.child); } return remove(empty); });
  await caseRun('configured-empty-repository', async () => { const claims = await freshClaims(empty, 'empty-store'); await install(empty, '17.0.1', claims); const a = await observed(empty, 'Ready'); const e = await endpoint(empty); try { assert.ok((await request(e, '/api/v1/version')).version.startsWith('17.0.1')); } finally { await closeChild(e.child); } await remove(empty); return { claims, status: a.status }; });
  await caseRun('native-old-source-fixture', async () => { const claims = await freshClaims(blue, 'source'); await install(blue, '16.0.7', claims); const a = await observed(blue, 'Ready'); const e = await endpoint(blue); try { hostKey = await hostIdentity(e); await configureOIDC(e); await seed(e); } finally { await closeChild(e.child); } return { claim: a.spec.claim, hostKey }; });
  await caseRun('existing-restart-backup-inaccessible', async () => {
    const a = await app(blue), reader = await object(blue.namespace, 'secret', a.spec.backup.readerSecret), saved = reader.data;
    await patchSecret(blue.namespace, reader.metadata.name, { restPassword: Buffer.from(randomBytes(24).toString('hex')).toString('base64') });
    try {
      const pods = JSON.parse((await kube(blue.namespace, ['get', 'pods', '-o', 'json'])).text).items;
      const serving = pods.find(p => p.spec.containers.some(c => c.image === image && invocation(c).includes('serve'))); assert.ok(serving);
      await kube(blue.namespace, ['delete', 'pod', serving.metadata.name, '--wait=true']);
      await until('replacement serving pod Ready', async () => {
        const replacement = JSON.parse((await kube(blue.namespace, ['get', 'pods', '-o', 'json'])).text).items;
        requireNonfailed(await app(blue), 'replacement serving pod Ready');
        return replacement.some(p => p.metadata.uid !== serving.metadata.uid && p.spec.containers.some(c => invocation(c).includes('serve')) && p.status.conditions?.some(c => c.type === 'Ready' && c.status === 'True'));
      });
      await observed(blue, 'Ready'); const e = await endpoint(blue); try { await hostIdentity(e, hostKey); await verify(e, 'restart', '16.0.7'); } finally { await closeChild(e.child); }
    } finally { await patchSecret(blue.namespace, reader.metadata.name, saved); }
  });
  let sourceSnapshot;
  await caseRun('capture-and-stop-source', async () => { const a = await backup(blue); sourceSnapshot = a.status.backup.snapshot; await stop(blue); await noWriters(blue); return { snapshot: sourceSnapshot, claim: a.spec.claim }; });
  await caseRun('native-old-to-new-independent-pvc', async () => {
    const values = JSON.parse(await readFile(green.values, 'utf8')).services[green.instance || 'forgejo'];
    assert.ok(values.bootstrapSecret, 'configured green bootstrap Secret must be named');
    await kube(green.namespace, ['delete', 'secret', values.bootstrapSecret, '--ignore-not-found', '--wait=true']);
    const remaining = JSON.parse((await kube(green.namespace, ['get', 'secrets', '--field-selector', `metadata.name=${values.bootstrapSecret}`, '-o', 'json'])).text);
    assert.equal(remaining.items.length, 0, 'configured green bootstrap Secret must be absent before independent recovery');
    missingBootstrap.add(green.namespace);
    const source = await app(blue), claims = await freshClaims(green, 'upgrade'), started = performance.now(); await install(green, '17.0.1', claims); const target = await observed(green, 'Ready');
    timings.push({ kind: 'restore-native-upgrade', namespace: green.namespace, installToReadyMs: Math.round(performance.now() - started), version: '17.0.1' }); emit('timing', timings.at(-1));
    assert.deepEqual(source.spec.identity, target.spec.identity);
    assert.notEqual((await object(blue.namespace, 'pvc', source.spec.claim)).metadata.uid, (await object(green.namespace, 'pvc', target.spec.claim)).metadata.uid);
    const e = await endpoint(green); try { await hostIdentity(e, hostKey); await verify(e, 'native-upgrade', '17.0.1'); } finally { await closeChild(e.child); }
    await noWriters(blue); return { sourceSnapshot, sourcePVC: source.spec.claim, targetPVC: target.spec.claim, bootstrapSecretRemoved: values.bootstrapSecret, status: target.status };
  });
  await caseRun('publication-failure-resume-retained-capture', async () => {
    const a = await app(green), writer = await object(green.namespace, 'secret', a.spec.backup.writerSecret), saved = writer.data;
    await patchSecret(green.namespace, writer.metadata.name, { restPassword: Buffer.from(randomBytes(24).toString('hex')).toString('base64') });
    let failed;
    try { failed = await backup(green, { success: false }); await observed(green, 'Ready'); assert.ok(condition(failed, 'BackupFailed')); }
    finally { await patchSecret(green.namespace, writer.metadata.name, saved); }
    const jobsBefore = JSON.parse((await kube(green.namespace, ['get', 'jobs', '-o', 'json'])).text).items.filter(j => j.spec.template.spec.containers.some(c => invocation(c).includes('capture'))).map(j => j.metadata.uid);
    const resumed = await backup(green); assert.equal(resumed.status.backup.operation, failed.status.backup.operation, 'publication retry must retain original capture operation');
    const jobsAfter = JSON.parse((await kube(green.namespace, ['get', 'jobs', '-o', 'json'])).text).items.filter(j => j.spec.template.spec.containers.some(c => invocation(c).includes('capture'))).map(j => j.metadata.uid);
    assert.deepEqual(jobsAfter, jobsBefore, 'publication retry must not create a replacement capture');
    return { failed: failed.status.backup, resumed: resumed.status.backup };
  });
  await caseRun('same-version-replacement-new-backup', async () => {
    const point = (await backup(green)).status.backup; await stop(green); await noWriters(green);
    await remove(blue); const claims = await freshClaims(blue, 'same-version'), started = performance.now(); await install(blue, '17.0.1', claims); const restored = await observed(blue, 'Ready');
    timings.push({ kind: 'same-version-restore', namespace: blue.namespace, installToReadyMs: Math.round(performance.now() - started), version: '17.0.1' }); emit('timing', timings.at(-1));
    const e = await endpoint(blue); try { await hostIdentity(e, hostKey); await verify(e, 'same-version', '17.0.1'); } finally { await closeChild(e.child); }
    const newPoint = (await backup(blue)).status.backup; assert.notEqual(newPoint.snapshot, point.snapshot); await noWriters(green);
    await stop(blue); await noWriters(blue);
    return { point, restored: restored.status, newPoint, claims };
  });
  await caseRun('same-version-blue-to-green-independent-pvc', async () => {
    const source = await app(blue), point = source.status.backup;
    await remove(green); const claims = await freshClaims(green, 'blue-to-green'), started = performance.now();
    await install(green, '17.0.1', claims); const target = await observed(green, 'Ready');
    timings.push({ kind: 'blue-to-green-restore', namespace: green.namespace, installToReadyMs: Math.round(performance.now() - started), version: '17.0.1' }); emit('timing', timings.at(-1));
    assert.deepEqual(source.spec.identity, target.spec.identity);
    assert.notEqual((await object(blue.namespace, 'pvc', source.spec.claim)).metadata.uid, (await object(green.namespace, 'pvc', target.spec.claim)).metadata.uid);
    const e = await endpoint(green); try { await hostIdentity(e, hostKey); await verify(e, 'blue-to-green', '17.0.1'); } finally { await closeChild(e.child); }
    const newPoint = (await backup(green)).status.backup; assert.notEqual(newPoint.snapshot, point.snapshot);
    await stop(green); await noWriters(blue); await noWriters(green);
    return { point, claims, newPoint };
  });
  await caseRun('native-downgrade-rejected-without-state-change', async () => {
    const source = await app(green), inputsBefore = await recoveryInputDigest(green);
    const claims = await freshClaims(empty, 'downgrade');
    await install(empty, '17.0.1', { ...claims, identity: source.spec.identity }); await observed(empty, 'Ready');
    const e = await endpoint(empty);
    try {
      assert.ok((await request(e, '/api/v1/version')).version.startsWith('17.0.1'));
      assert.equal((await request(e, `${repoPath(e)}/issues/1`)).title, 'Preserved issue');
      await hostIdentity(e, hostKey);
    } finally { await closeChild(e.child); }
    await stop(empty); await noWriters(empty);
    const before = await readOnlyStateDigest(empty, claims.claim);
    await patch(empty, { version: '16.0.7', replicas: 1, ingress: await slotIngress(empty) });
    const failed = await observed(empty, 'Failed');
    assert.equal(condition(failed, 'Ready'), false, 'downgrade must not become Ready');
    await noWriters(empty);
    const after = await readOnlyStateDigest(empty, claims.claim);
    assert.equal(after, before, 'rejected downgrade must not change native SQLite or any owned file content');
    assert.deepEqual(await recoveryInputDigest(green), inputsBefore, 'downgrade must leave original recovery inputs unchanged');
    await remove(empty); // Retains the failed state; later faults use fresh PVCs.
    return { claims, beforeSHA256: before, afterSHA256: after, recoveryInputs: inputsBefore, status: failed.status };
  });
  }
  if (!continuation?.removalOnly) {
  if (!continuation?.malformedEvidence) {
    await caseRun('malformed-lookup-fails-closed', () => failedTarget(empty, 'malformed', 'malformed'));
  }
  await caseRun('corrupt-selected-point-no-fallback', () => failedTarget(empty, 'corrupt', 'corrupt'));
  await caseRun('interrupted-mutation-remains-stopped', () => failedTarget(empty, 'interrupted', 'hold'));
  await caseRun('stop-overrides-operation-completion', async () => {
    await start(green);
    setFault('hold'); const requestID = `stop-${Date.now()}`;
    await kube(green.namespace, ['annotate', 'forgejoapplication', 'forgejo', `forgejo.bartl.sh/backup-request=${requestID}`, '--overwrite']);
    await until('publication readback barrier', async () => {
      const a = await app(green); requireNonfailed(a, 'publication readback barrier');
      return a.status?.operation?.kind === 'publish' && fault.hits > 0;
    });
    await patch(green, { replicas: 0, ingress: { enabled: false } });
    setFault('normal'); const a = await observed(green, 'Stopped'); await noWriters(green, 10000); return { status: a.status };
  });
  }
  await caseRun('removal-retained-state-reinstall', async () => {
    // Stop-race evidence above remains separate. Finish the retained publication
    // on its original CR and claims before exercising supported planned removal.
    const predecessor = await app(green);
    const operation = continuation?.removalPredecessor?.operation || predecessor.status?.operation;
    assert.equal(operation?.kind, 'publish', 'planned removal requires the original publication operation');
    assert.ok(operation.id, 'original capture operation identity required');
    assert.equal(operation.claim, predecessor.spec.claim);
    assert.equal(operation.stagingClaim, predecessor.spec.stagingClaim);
    assert.equal(operation.version, predecessor.spec.version);
    const captureJobs = async () => JSON.parse((await kube(green.namespace, ['get', 'jobs', '-o', 'json'])).text).items
      .filter(j => j.spec.template.spec.containers.some(c => invocation(c).includes('capture'))).map(j => j.metadata.uid).sort();
    const jobsBefore = await captureJobs();
    await start(green);
    let finished = await app(green);
    if (finished.status?.backup?.operation !== operation.id || !finished.status.backup.success) {
      if (finished.status?.operation?.phase === 'Failed') {
        const finishRequest = `before-removal-${Date.now()}-${randomBytes(4).toString('hex')}`;
        finished = await backup(green, { request: finishRequest });
      } else if (!continuation?.removalOnly) {
        finished = await until('original capture publication after start', async () => {
          const current = await app(green); requireNonfailed(current, 'original capture publication');
          return current.status?.backup?.operation === operation.id && current.status.backup.success && current;
        });
      }
    }
    assert.equal(finished.metadata.uid, predecessor.metadata.uid, 'publication must finish on the original CR');
    const originalPublication = await publishedCapture(green, predecessor, operation);
    if (!continuation?.removalOnly) {
      assert.equal(finished.status.backup.operation, operation.id, 'publication must preserve the original capture operation');
      assert.equal(finished.status.operation.id, operation.id);
    }
    assert.equal(finished.status.operation.kind, 'publish');
    assert.equal(finished.status.operation.phase, 'Succeeded');
    assert.equal(finished.status.backup.success, true);
    assert.deepEqual(await captureJobs(), jobsBefore, 'finishing publication must not replace the retained capture');
    const a = await stop(green); await noWriters(green);
    const retained = await remove(green);
    await install(green, '17.0.1', { claim: a.spec.claim, stagingClaim: a.spec.stagingClaim }); await observed(green, 'Ready');
    const e = await endpoint(green); try { await hostIdentity(e, hostKey); await verify(e, 'reinstall', '17.0.1'); } finally { await closeChild(e.child); }
    const requestID = `reinstall-${Date.now()}-${randomBytes(4).toString('hex')}`;
    const published = await backup(green, { request: requestID });
    assert.equal(published.status.backup.request, requestID);
    assert.equal(published.status.operation.kind, 'publish');
    assert.equal(published.status.operation.phase, 'Succeeded');
    assert.ok(published.status.conditions.some(c => c.type === 'BackupFailed' && c.status === 'False' && c.reason === 'Published'),
      'new retained-state backup must reach verified publication');
    await stop(green); await noWriters(green);
    const claims = await freshClaims(empty, 'reinstall-backup');
    await install(empty, '17.0.1', { ...claims, identity: published.spec.identity });
    const recovered = await observed(empty, 'Ready'), restored = await endpoint(empty);
    try {
      await hostIdentity(restored, hostKey);
      assert.ok((await request(restored, '/api/v1/version')).version.startsWith('17.0.1'));
      const issues = await request(restored, `${repoPath(restored)}/issues?state=all&type=issues&limit=50`);
      assert.ok(issues.some(issue => issue.title === 'reinstall new work'), 'new issue must survive reinstall backup recovery');
      assert.deepEqual(await request(restored, `/api/packages/${restored.username}/generic/package-proof/reinstall/proof.txt`, { raw: true }), fixtureBytes);
      for (const [path, content] of [['reinstall.txt', 'reinstall HTTP write\n'], ['ssh-reinstall.txt', 'reinstall SSH write\n']]) {
        const file = await request(restored, `${repoPath(restored)}/contents/${path}?ref=main`);
        assert.equal(Buffer.from(file.content, 'base64').toString(), content, 'new Git work must survive reinstall backup recovery');
      }
    } finally { await closeChild(restored.child); }
    await stop(empty); await noWriters(empty); await remove(empty);
    return { ...retained, predecessorUID: predecessor.metadata.uid, originalCaptureOperation: operation.id,
      originalPublication, beforeRemovalBackup: finished.status.backup, backup: published.status.backup, recoveredClaim: recovered.spec.claim,
      recoveredWork: ['reinstall issue', 'reinstall package', 'reinstall HTTP and SSH commits'] };
  });
  return identity;
}
let outcome = 'failed', failure, identity;
try { identity = await main(); outcome = 'passed'; }
catch (error) { failure = error.stack; process.exitCode = 1; }
finally {
  setFault('normal');
  if (work) await diagnostics();
  for (const child of [...children]) await closeChild(child);
  if (proxy) { proxy.closeAllConnections(); await new Promise(done => proxy.close(done)); }
  if (work) {
    const report = { outcome, completed: new Date().toISOString(), workdir: work, identity: identity || (await readFile(join(work, 'artifact-identity.json'), 'utf8').then(JSON.parse).catch(() => null)), continuation, results, snapshots, timings, failure };
    await privateWrite(join(work, 'result.json'), JSON.stringify(report, null, 2)); emit('result', report);
  } else emit('result', { outcome, failure });
}
