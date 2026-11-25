#!/usr/bin/env node
// Offline package interface: no cluster, registry, credentials or preparation tooling.
import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
const chart = process.env.FORGEJO_CHART || fileURLToPath(new URL('../chart', import.meta.url));
function render(values, {mode = 'deployment', release = 'forgejo', namespace = 'default', deployment} = {}) {
  const args = ['template', release, chart, '-n', namespace, '-f', '-', '--set-string', `renderMode=${mode}`];
  if (deployment) args.push('--set-string', `deployment=${deployment}`);
  return spawnSync(process.env.HELM || 'helm', args, {input: JSON.stringify(values), encoding: 'utf8', maxBuffer: 8 * 1024 * 1024});
}
function documents(result) {
  assert.equal(result.status, 0, result.stderr);
  const parsed = spawnSync(process.env.YQ || 'yq', ['-o=json', '-I=0', '.'], {input: result.stdout, encoding: 'utf8'});
  assert.equal(parsed.status, 0, parsed.stderr);
  return parsed.stdout.trim().split('\n').filter(Boolean).map(JSON.parse).filter(Boolean);
}
const base = () => ({installation: 'acme', dns: {root: 'acme.example'}, services: {forgejo: {}}});
const service = v => v.services[v.instance || 'forgejo'];
const selection = v => v.deployments?.[v.deployment];
const instance = v => selection(v)?.instance || v.instance || 'forgejo';
const docs = v => documents(render(v, {release: selection(v)?.release || 'forgejo', namespace: selection(v)?.namespace || 'default', deployment: v.deployment}));
const spec = v => docs(v).find(d => d.kind === 'ForgejoApplication').spec;
const declaration = v => documents(render(v, {mode: 'connections', release: selection(v)?.release || 'offline', namespace: selection(v)?.namespace || 'default', deployment: v.deployment}))[0];
const configuration = v => documents(render(v, {mode: 'configuration', release: selection(v)?.release || 'offline', namespace: selection(v)?.namespace || 'default', deployment: v.deployment}))[0].configuration[instance(v)];
function rejects(v, options) { assert.notEqual(render(v, options).status, 0, 'invalid interface rendered successfully'); }
let checks = 0;
function check(name, fn) { fn(); checks++; console.log('PASS ' + name); }
check('shared file selects only this independent release', () => {
  const v = base(); v.services.keycloak = {unrelated: ['ignored']}; v.infrastructure = {foreign: true};
  v.resolved = {oidc: {bindings: {other: 'ignored'}}};
  const app = spec(v);
  assert.deepEqual(app.identity, {installation: 'acme', instance: 'forgejo'});
  assert.equal(app.version, '17.0.1'); assert.equal(app.claim, 'forgejo-state'); assert.equal(app.stagingClaim, 'forgejo-capture');
  assert.equal(app.ingress.host, 'forgejo.acme.example');
  assert.equal(docs(v).some(d => d.kind === 'Secret'), false);
});
check('custom identity and map overrides retain defaults and explicit zero/false', () => {
  const v = base(); v.instance = 'git'; v.services = {forgejo: 'ignored', git: {replicas: 0, claims: {state: 'git-state'}, ingress: {enabled: false}, controller: {imagePullPolicy: 'Always', resources: {requests: {cpu: '40m'}}}}};
  const app = spec(v); assert.equal(app.identity.instance, 'git'); assert.equal(app.replicas, 0); assert.equal(app.claim, 'git-state'); assert.equal(app.stagingClaim, 'forgejo-capture'); assert.equal(app.ingress.enabled, false);
  const container = docs(v).find(d => d.kind === 'Deployment').spec.template.spec.containers[0];
  assert.equal(container.resources.requests.cpu, '40m'); assert.equal(container.resources.requests.memory, '64Mi'); assert.equal(container.imagePullPolicy, 'Always');
});
check('all renders derive current endpoints and honor hostname override', () => {
  const v = base(); service(v).hostname = 'git.example'; v.dns.root = 'new.example';
  for (const host of ['git.example', 'forgejo.new.example']) {
    assert.equal(spec(v).ingress.host, host); assert.equal(declaration(v).services.forgejo.endpoints.https.url, `https://${host}`);
    assert.ok(configuration(v).appIni.includes(`ROOT_URL = https://${host}/`)); assert.ok(configuration(v).appIni.includes(`SSH_DOMAIN = ${host}`)); delete service(v).hostname;
  }
  delete v.dns; rejects(v); service(v).hostname = 'git.example'; spec(v);
});
check('nonsecret native configuration owns every supported path and disabled effect', () => {
  const config = configuration(base()); assert.equal(config.secretName, 'forgejo-configuration'); assert.equal(config.key, 'app.ini');
  for (const line of ['RUN_USER = git', 'WORK_PATH = /data', 'PATH = /data/data/forgejo.db', 'ROOT = /data/data/repositories', 'SSH_LISTEN_PORT = 2222', 'SSH_SERVER_HOST_KEYS = ssh/forgejo.ed25519', 'DISABLE_WEBHOOKS = true', 'DISABLE_GIT_HOOKS = true', 'ACCOUNT_LINKING = disabled', 'PATH = /data/data/packages']) assert.ok(config.appIni.includes(line), line);
  for (const key of ['SECRET_KEY =', 'INTERNAL_TOKEN =', 'LFS_JWT_SECRET =']) assert.equal(config.appIni.includes(key), false);
  assert.deepEqual(config.retainedIdentity, {security: ['SECRET_KEY', 'INTERNAL_TOKEN'], server: ['LFS_JWT_SECRET']});
  assert.deepEqual(config.optionalClientCA, {environmentKey: 'SSL_CERT_FILE', bundleKey: 'ca-bundle.pem', bundlePath: '/configuration/ca-bundle.pem'});
});
check('offline unresolved declarations require no dependencies or deployment resources', () => {
  const v = base(); service(v).identityProvider = 'accounts'; service(v).backup = {endpoint: 'archive'};
  const d = declaration(v); assert.equal(d.kind, undefined); assert.equal(documents(render(v, {mode: 'connections', release: 'offline'})).length, 1);
  assert.equal(d.oidc.requests.forgejo.provider, 'accounts'); assert.deepEqual(d.oidc.requests.forgejo.redirectUris, ['https://forgejo.acme.example/user/oauth2/keycloak/callback']);
  assert.equal(d.backup.requests.forgejo.endpoint, 'archive'); assert.equal(d.storage.claims.forgejo.state.claimName, 'forgejo-state');
  assert.deepEqual(d.credentials.requirements.forgejo.backupReader.keys, ['password', 'username', 'restPassword']);
  rejects(v); rejects(v, {mode: 'configuration'});
});
check('standalone OIDC and composed matching binding own native setup inputs', () => {
  const v = base(); service(v).oidc = {issuer: 'https://login.example/realms/external', name: 'external', clientId: 'git'};
  let config = configuration(v).oidc; assert.equal(config.discoveryUrl, 'https://login.example/realms/external/.well-known/openid-configuration'); assert.equal(config.callbackUrl, 'https://forgejo.acme.example/user/oauth2/external/callback'); assert.equal(config.clientId, 'git'); assert.equal(config.clientSecretRequired, true); spec(v);
  delete service(v).oidc; service(v).identityProvider = 'accounts'; v.resolved = {oidc: {bindings: {forgejo: {provider: 'accounts', issuer: 'https://login.example/realms/acme'}}}};
  config = configuration(v).oidc; assert.equal(config.clientId, 'forgejo'); assert.equal(config.name, 'keycloak'); spec(v);
  v.resolved.oidc.bindings.forgejo.provider = 'wrong'; rejects(v); rejects(v, {mode: 'configuration'});
  v.resolved.oidc.bindings.forgejo.provider = 'accounts'; v.resolved.oidc.bindings.forgejo.clientId = 'wrong'; rejects(v);
});
check('OIDC unknown protected fields and ambiguous issuer fail', () => {
  for (const oidc of [{}, {issuer: 'http://insecure.example'}, {issuer: 'https://issuer.example', clientSecret: 'plaintext'}]) { const v = base(); service(v).oidc = oidc; rejects(v); }
  const v = base(); service(v).identityProvider = 'accounts'; service(v).oidc = {issuer: 'https://issuer.example'}; rejects(v, {mode: 'connections'});
});
check('backup eligibility does not activate an unbound deployment and explicit opt-out wins', () => {
  const v = base();
  const eligible = declaration(v);
  assert.equal(eligible.backup.requests.forgejo.enabled, true);
  assert.equal(eligible.backup.requests.forgejo.repository, undefined);
  assert.deepEqual(eligible.credentials.requirements.forgejo.backupReader.keys, ['password', 'username', 'restPassword']);
  assert.equal(spec(v).backup, undefined);
  service(v).backup = {enabled: false};
  v.resolved = {backup: {bindings: {forgejo: {repository: 'rest:https://old.example/forgejo/'}}}};
  const disabled = declaration(v);
  assert.deepEqual(disabled.backup.requests.forgejo, {enabled: false});
  assert.equal(disabled.credentials.bindings.forgejo.backupReader, undefined);
  assert.equal(disabled.credentials.requirements.forgejo.backupWriter, undefined);
  assert.equal(spec(v).backup, undefined);
});
check('external and composed backups preserve controller API and credential roles', () => {
  const v = base(); service(v).backup = {repository: 'rest:https://backup.example/forgejo/', schedule: '0 3 * * *', timeZone: 'Europe/Berlin'};
  assert.deepEqual(spec(v).backup, {repository: 'rest:https://backup.example/forgejo/', readerSecret: 'forgejo-backup-reader', writerSecret: 'forgejo-backup-writer', schedule: '0 3 * * *', timeZone: 'Europe/Berlin'});
  service(v).backup = {endpoint: 'archive'}; v.resolved = {backup: {bindings: {forgejo: {endpoint: 'archive', repository: 'rest:https://archive.example/forgejo/', readerSecret: 'reader', writerSecret: 'writer'}, other: 'ignored'}}};
  const b = spec(v).backup; assert.equal(b.readerSecret, 'reader'); assert.equal(b.writerSecret, 'writer'); assert.equal(b.endpoint, undefined); assert.equal(b.enabled, undefined);
  service(v).backup.enabled = false; assert.equal(declaration(v).credentials.requirements.forgejo.backupReader, undefined);
});
check('manual-only backup omits the CRD-constrained schedule', () => {
  const v = base(); service(v).backup = {repository: 'rest:https://backup.example/forgejo/'};
  assert.equal(Object.hasOwn(spec(v).backup, 'schedule'), false);
  service(v).backup.schedule = '';
  assert.equal(Object.hasOwn(spec(v).backup, 'schedule'), false);
});
check('unresolved conflicting and credential-bearing backup locations fail', () => {
  const v = base(); service(v).backup = {endpoint: 'archive'}; rejects(v);
  v.resolved = {backup: {bindings: {forgejo: {endpoint: 'wrong', repository: 'rest:https://backup.example/forgejo/'}}}}; rejects(v);
  for (const b of [{enabled: true}, {repository: 'rest:https://user:password@backup.example/forgejo/'}, {endpoint: 'archive', repository: 'rest:https://backup.example/forgejo/'}]) { delete v.resolved; service(v).backup = b; rejects(v); }
});
check('managed cert-manager TLS and independently supplied TLS are distinct', () => {
  const v = base(); service(v).ingress = {enabled: false, className: 'edge', tlsSecret: 'git-tls'};
  const resources = docs(v); const cert = resources.find(d => d.kind === 'Certificate'); const issuer = resources.find(d => d.kind === 'Issuer');
  assert.equal(cert.spec.secretName, 'git-tls'); assert.deepEqual(cert.spec.dnsNames, ['forgejo.acme.example']); assert.equal(cert.spec.issuerRef.name, issuer.metadata.name); assert.equal(issuer.spec.acme.solvers[0].http01.ingress.ingressClassName, 'edge');
  service(v).tls = {managed: false}; assert.equal(docs(v).some(d => d.kind === 'Certificate'), false);
  service(v).ingress.enabled = true; assert.deepEqual(declaration(v).credentials.requirements.forgejo.tls.sources, ['external', 'recovered']);
  service(v).ingress.tlsSecret = ''; rejects(v);
});
check('native SSH Service exposes only serving pods and advertises explicit NodePort', () => {
  const v = base(); let ssh = docs(v).find(d => d.kind === 'Service' && d.metadata.name === 'forgejo-ssh');
  assert.equal(ssh.spec.type, 'LoadBalancer'); assert.equal(ssh.spec.ports[0].targetPort, 2222); assert.equal(ssh.spec.selector['forgejo.bartl.sh/role'], 'app');
  service(v).ssh = {exposure: 'NodePort', nodePort: 30222, hostname: 'ssh.example'};
  ssh = docs(v).find(d => d.kind === 'Service' && d.metadata.name === 'forgejo-ssh'); assert.equal(ssh.spec.ports[0].nodePort, 30222);
  assert.ok(configuration(v).appIni.includes('SSH_PORT = 30222')); assert.equal(declaration(v).services.forgejo.endpoints.ssh.port, 30222);
  delete service(v).ssh.nodePort; rejects(v); service(v).ssh = {nodePort: 30222}; rejects(v);
});
check('exact release consistency remains valid for standalone instance selection', () => {
  const v = base(); service(v).release = '0.2.3'; spec(v); declaration(v); configuration(v);
  service(v).release = '0.1.0'; for (const mode of ['deployment', 'connections', 'configuration']) rejects(v, {mode});
});
check('selected deployment resolves its own instance, intent and all renders', () => {
  const v = base(); v.deployment = 'target';
  v.deployments = {target: {instance: 'git', namespace: 'green', release: 'git-target', package: 'new'}, source: {instance: 'git', namespace: 'blue', release: 'git-source', package: 'old'}};
  v.packages = {new: {version: '0.2.3'}, old: {version: '0.2.3'}};
  v.services = {forgejo: {unknown: 'ignored'}, git: {replicas: 1, version: '16.0.7', ingress: {enabled: true}, hostname: 'source.example', backup: {enabled: false}}};
  v.operations = {deployments: {target: {replicas: 0, ingressEnabled: false, version: '17.0.1', hostname: 'target.example'}, source: {replicas: 1, ingressEnabled: true, version: '16.0.7', hostname: 'source.example'}}};
  const app = spec(v);
  assert.deepEqual(app.identity, {installation: 'acme', instance: 'git'});
  assert.equal(app.replicas, 0); assert.equal(app.ingress.enabled, false); assert.equal(app.version, '17.0.1'); assert.equal(app.ingress.host, 'target.example');
  assert.equal(declaration(v).services.git.endpoints.https.url, 'https://target.example');
  assert.ok(configuration(v).appIni.includes('ROOT_URL = https://target.example/'));
  v.deployment = 'source'; assert.equal(spec(v).version, '16.0.7'); assert.equal(spec(v).ingress.host, 'source.example');
  assert.equal(declaration(v).services.git.endpoints.https.url, 'https://source.example');
  assert.ok(configuration(v).appIni.includes('ROOT_URL = https://source.example/'));
});
check('selected nondefault identity uses only its OIDC and backup bindings', () => {
  const v = base(); v.deployment = 'target';
  v.deployments = {target: {instance: 'git', namespace: 'green', release: 'git-target', package: 'new'}};
  v.packages = {new: {version: '0.2.3'}};
  v.services = {forgejo: {unknown: true}, git: {identityProvider: 'accounts', backup: {endpoint: 'archive'}}};
  v.resolved = {
    oidc: {bindings: {forgejo: {provider: 'wrong'}, git: {provider: 'accounts', issuer: 'https://login.example/realms/acme'}}},
    backup: {bindings: {forgejo: {endpoint: 'wrong'}, git: {endpoint: 'archive', repository: 'rest:https://backup.example/git/'}}}
  };
  assert.equal(spec(v).backup.repository, 'rest:https://backup.example/git/');
  assert.equal(configuration(v).oidc.clientId, 'git');
  assert.equal(declaration(v).oidc.requests.git.clientId, 'git');
  delete v.resolved.oidc.bindings.git; rejects(v, {deployment: 'target', release: 'git-target', namespace: 'green'});
});
check('selected deployment rejects mismatched identity, namespace, release, package and intent', () => {
  const v = base(); v.services.git = {}; v.deployments = {target: {instance: 'git', namespace: 'green', release: 'git-target', package: 'new'}};
  v.packages = {new: {version: '0.2.3'}};
  const opts = {deployment: 'target', release: 'git-target', namespace: 'green'};
  documents(render(v, opts));
  rejects(v, {...opts, namespace: 'blue'}); rejects(v, {...opts, release: 'wrong'});
  v.instance = 'forgejo'; rejects(v, opts); delete v.instance;
  v.packages.new.version = '0.2.2'; rejects(v, opts); v.packages.new.version = '0.2.3';
  delete v.services.git; rejects(v, opts); v.services.git = {};
  v.operations = {deployments: {target: {replicas: 2}}}; rejects(v, opts);
  v.operations.deployments.target = {ingressEnabled: 'false'}; rejects(v, opts);
  v.operations.deployments.target = {version: '18.0.0'}; rejects(v, opts);
  v.operations.deployments.target = {hostname: 'https://bad.example'}; rejects(v, opts);
  v.operations.deployments.target = {replicas: 0, ingressEnabled: false};
  assert.equal(documents(render(v, opts)).find(d => d.kind === 'ForgejoApplication').spec.replicas, 0);
});
check('strict selected interface rejects legacy inputs invalid types and unknown fields', () => {
  for (const [key, value] of Object.entries({identity: {}, image: 'legacy', version: '17.0.1', claim: 'state', stagingClaim: 'stage', replicas: 1, ssh: {}, controller: {}})) { const v = base(); v[key] = value; rejects(v); }
  for (const s of [false, [], 'invalid', {unknown: true}, {replicas: 0.5}, {replicas: 2}, {version: '18.0.0'}, {claims: {staging: 'forgejo-state'}}, {ingress: {enabled: 'false'}}, {controller: {image: 'mutable:latest'}}, {ssh: {port: 0}}, {hostname: 'https://invalid.example'}]) { const v = base(); v.services.forgejo = s; rejects(v); }
  const v = base(); v.services.other = {invalid: true}; v.backup = {endpoints: {foreign: {unknown: true}}}; declaration(v); configuration(v); spec(v);
});
console.log(`PASS ${checks} chart behavior checks`);
