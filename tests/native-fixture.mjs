#!/usr/bin/env node
// SPDX-License-Identifier: Apache-2.0
// All runtime state belongs to the caller's disposable, remote-only workdir.
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import { chmod, mkdir, readdir, readFile, writeFile, open, lstat, readlink, rename } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { userInfo } from 'node:os';
import { setTimeout as delay } from 'node:timers/promises';

const releases = {
  '16.0.7': 'ca8ecb37da9210d5730c878308bf31e25b6b526a7294b263d07f6c8718af07b7',
  '17.0.1': '51a8cdf4a1252e79f4b3855d7a7891af6c6635105420768c460e2f0690385d6f',
};
const work = resolve(process.argv[2] || '.');
const httpPort = Number(process.env.BARTL_NATIVE_HTTP_PORT || 3300);
const sshPort = Number(process.env.BARTL_NATIVE_SSH_PORT || 3322);
const base = `http://127.0.0.1:${httpPort}`;
const emit = (event, fields = {}) => console.log(JSON.stringify({ event, ...fields }));
const hash = bytes => createHash('sha256').update(bytes).digest('hex');
let running;
let token;
let sequence = 0;
let currentSlot;
const password = `Native-9!${randomBytes(20).toString('hex')}`;
const secrets = { secret: randomBytes(32).toString('hex'), internal: randomBytes(32).toString('hex'), lfs: randomBytes(32).toString('base64url') };
const basic = `Basic ${Buffer.from(`proof-admin:${password}`).toString('base64')}`;
const fixtureBytes = Buffer.from('bartl native recovery attachment and package\n');
const lfsBytes = Buffer.from('bartl native large file payload\n'.repeat(4096));
const source = join(work, 'source');
const protectedSource = join(work, 'protected-source');
let sourceParked = false;
const binary = version => join(work, 'bin', `forgejo-${version}`);

// Do not hand cloud/publication credentials or unrelated host configuration to
// the application or Git hooks. The proof has no production dependencies.
function childEnv() {
  return Object.fromEntries(['PATH', 'HOME', 'LANG', 'TMPDIR', 'XDG_CACHE_HOME', 'USER', 'LOGNAME']
    .filter(key => process.env[key] !== undefined).map(key => [key, process.env[key]]));
}
async function command(program, args, { cwd = work, env = {}, nativeSlot, sensitive = false, allowFailure = false } = {}) {
  const id = String(++sequence).padStart(4, '0');
  const result = await new Promise((resolveCommand, reject) => {
    const child = spawn(program, args, { cwd, env: { ...childEnv(), ...(nativeSlot ? nativeEnv(nativeSlot) : gitEnv), ...env }, stdio: ['ignore', 'pipe', 'pipe'] });
    const stdout = [], stderr = [];
    child.stdout.on('data', chunk => stdout.push(chunk));
    child.stderr.on('data', chunk => stderr.push(chunk));
    child.once('error', reject);
    child.once('close', code => resolveCommand({ code, stdout: Buffer.concat(stdout), stderr: Buffer.concat(stderr) }));
  });
  // Raw logs are deliberately private: CLI token generation and HTTP Git may contain credentials.
  await writeFile(join(work, 'raw', `${id}.stdout`), result.stdout, { mode: 0o600 });
  await writeFile(join(work, 'raw', `${id}.stderr`), result.stderr, { mode: 0o600 });
  emit('command', { id, program: program.split('/').at(-1), arguments: sensitive ? '[private]' : args, exit: result.code });
  if (!allowFailure && result.code !== 0) throw new Error(`Command ${id} failed (${result.code}); inspect private raw logs`);
  return result.stdout.toString().trim();
}
const gitEnv = {
  GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: '/dev/null', GIT_TERMINAL_PROMPT: '0',
  GIT_AUTHOR_NAME: 'Native proof', GIT_AUTHOR_EMAIL: 'native-proof@example.invalid',
  GIT_COMMITTER_NAME: 'Native proof', GIT_COMMITTER_EMAIL: 'native-proof@example.invalid',
};
// Native initialization writes global Git defaults. Give each server slot its
// own writable HOME/config, without the client author's identity overrides.
function nativeEnv(slot) {
  const home = join(slot, 'home');
  return {
    HOME: home, XDG_CONFIG_HOME: join(home, '.config'), XDG_CACHE_HOME: join(home, '.cache'),
    GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: join(home, '.gitconfig'), GIT_TERMINAL_PROMPT: '0',
  };
}
async function cli(slot, version, args, options = {}) {
  return command(binary(version), ['--work-path', slot, '--config', join(slot, 'app.ini'), ...args], { ...options, nativeSlot: slot });
}
async function config(slot) {
  await mkdir(join(slot, 'home'), { recursive: true });
  // Paths are target configuration, not recovered identity. Every mutable native
  // path is within this slot; no restored process may reference source storage.
  const text = `APP_NAME = Bartl native qualification\nRUN_USER = ${userInfo().username}\nRUN_MODE = prod\nWORK_PATH = ${slot}\n
[server]
APP_DATA_PATH = ${slot}/data
HTTP_ADDR = 127.0.0.1
HTTP_PORT = ${httpPort}
DOMAIN = 127.0.0.1
ROOT_URL = ${base}/
LOCAL_ROOT_URL = ${base}/
OFFLINE_MODE = true
START_SSH_SERVER = true
SSH_LISTEN_HOST = 127.0.0.1
SSH_LISTEN_PORT = ${sshPort}
SSH_PORT = ${sshPort}
SSH_DOMAIN = 127.0.0.1
SSH_USER = git
BUILTIN_SSH_SERVER_USER = git
SSH_ROOT_PATH = ${slot}/data/ssh
SSH_SERVER_HOST_KEYS = ssh/forgejo.ed25519
LFS_START_SERVER = true
LFS_JWT_SECRET = ${secrets.lfs}
[database]
DB_TYPE = sqlite3
PATH = ${slot}/data/forgejo.db
[repository]
ROOT = ${slot}/data/repositories
[security]
INSTALL_LOCK = true
SECRET_KEY = ${secrets.secret}
INTERNAL_TOKEN = ${secrets.internal}
[service]
DISABLE_REGISTRATION = true
REQUIRE_SIGNIN_VIEW = true
ENABLE_NOTIFY_MAIL = false
[mailer]
ENABLED = false
[actions]
ENABLED = false
[mirror]
ENABLED = false
[federation]
ENABLED = false
[webhook]
ALLOWED_HOST_LIST = loopback
[log]
MODE = console
LEVEL = Info
ROOT_PATH = ${slot}/log
`;
  await writeFile(join(slot, 'app.ini'), text, { mode: 0o600 });
}
async function request(path, { method = 'GET', body, auth = token ? `token ${token}` : basic, headers = {}, expected = [200], raw = false } = {}) {
  const requestHeaders = { Authorization: auth, ...headers };
  if (body !== undefined && !(body instanceof FormData) && !Buffer.isBuffer(body)) {
    requestHeaders['Content-Type'] = 'application/json';
    body = JSON.stringify(body);
  }
  const response = await fetch(`${base}${path}`, { method, headers: requestHeaders, body, signal: AbortSignal.timeout(30000) });
  const bytes = Buffer.from(await response.arrayBuffer());
  const id = `http-${String(++sequence).padStart(4, '0')}`;
  await writeFile(join(work, 'raw', `${id}.body`), bytes, { mode: 0o600 });
  emit('http', { id, method, path, status: response.status, sha256: hash(bytes) });
  assert.ok(expected.includes(response.status), `${method} ${path}: ${response.status}; see ${id}`);
  return raw ? bytes : bytes.length ? JSON.parse(bytes) : null;
}
async function start(slot, version) {
  assert.equal(running, undefined, 'only one isolated slot may run');
  currentSlot = slot;
  const log = await open(join(work, 'raw', `${slot.split('/').at(-1)}-${version}-${sequence++}.log`), 'a', 0o600);
  const child = spawn(binary(version), ['--work-path', slot, '--config', join(slot, 'app.ini'), 'web'], {
    cwd: slot, env: { ...childEnv(), ...nativeEnv(slot) }, stdio: ['ignore', log.fd, log.fd],
  });
  running = child;
  child.on('error', () => {});
  await log.close();
  for (let attempt = 0; attempt < 180; attempt++) {
    if (child.exitCode !== null || child.signalCode !== null) throw new Error(`Forgejo ${version} exited during native startup`);
    let response;
    try {
      response = await fetch(`${base}/api/v1/version`, {
        headers: { Authorization: token ? `token ${token}` : basic },
        signal: AbortSignal.timeout(1000),
      });
    } catch { /* Transport may fail while native initialization/migration runs. */ }
    if (response?.ok) {
      const result = await response.json();
      assert.ok(result.version.startsWith(version), `unexpected version: ${result.version}`);
      emit('native-ready', { slot, version: result.version });
      return;
    }
    await delay(500);
  }
  throw new Error(`Native readiness timed out for ${slot}`);
}
async function stop() {
  if (!running) return;
  const child = running;
  child.kill('SIGTERM');
  const code = await Promise.race([
    new Promise(resolveExit => child.exitCode !== null ? resolveExit(child.exitCode) : child.once('exit', resolveExit)),
    delay(60000, undefined, { ref: false }).then(() => { throw new Error('Graceful stop timed out; capture forbidden'); }),
  ]);
  assert.equal(code, 0, 'native graceful shutdown must succeed before capture');
  running = undefined;
  emit('native-stopped', { slot: currentSlot });
}
async function treeDigest(root) {
  const digest = createHash('sha256');
  async function visit(directory, prefix = '') {
    for (const name of (await readdir(directory)).sort()) {
      const path = join(directory, name), relative = `${prefix}${name}`;
      const info = await lstat(path);
      digest.update(`${relative}\0${info.mode}\0`);
      if (info.isDirectory()) await visit(path, `${relative}/`);
      else if (info.isSymbolicLink()) digest.update(await readlink(path));
      else digest.update(await readFile(path));
    }
  }
  await visit(root);
  return digest.digest('hex');
}
async function capture(slot, name) {
  assert.equal(running, undefined, 'capture requires confirmed stopped native process');
  const artifact = join(work, `${name}.tar`);
  await command('tar', ['--format=posix', '-cf', artifact, '-C', slot, '.']);
  const sha256 = hash(await readFile(artifact));
  await writeFile(`${artifact}.sha256`, `${sha256}  ${name}.tar\n`);
  emit('artifact', { kind: 'stopped-native-files', artifact, sha256 });
  return artifact;
}
async function storedIdentity(slot) {
  const text = await readFile(join(slot, 'app.ini'), 'utf8');
  const identity = {};
  for (const key of ['SECRET_KEY', 'INTERNAL_TOKEN', 'LFS_JWT_SECRET']) {
    const matches = text.split('\n').map(line => line.match(/^([A-Z_]+)\s*=\s*(.+)$/)).filter(match => match?.[1] === key);
    assert.equal(matches.length, 1, `stored ${key} must occur exactly once`);
    identity[key] = hash(matches[0][2].trim());
  }
  for (const key of ['forgejo.ed25519', 'forgejo.ed25519.pub']) {
    identity[key] = hash(await readFile(join(slot, 'data/ssh', key)));
  }
  return identity;
}
async function restore(artifact, slot, expectedIdentity) {
  await mkdir(slot); // Must be new: never overlay ambiguous existing state.
  await command('tar', ['-xf', artifact, '-C', slot]);
  assert.deepEqual(await storedIdentity(slot), expectedIdentity, 'recovered stored identity differs from capture');
  const original = await readFile(join(slot, 'app.ini'), 'utf8');
  const pathKeys = new Set(['WORK_PATH', 'APP_DATA_PATH', 'SSH_ROOT_PATH', 'PATH', 'ROOT', 'ROOT_PATH']);
  const relocated = new Set();
  const text = original.split('\n').map(line => {
    const match = line.match(/^([A-Z_]+)(\s*=\s*)(.*)$/);
    if (!match || !pathKeys.has(match[1])) return line;
    assert.ok(match[3] === source || match[3].startsWith(`${source}/`), `unexpected captured path for ${match[1]}`);
    assert.ok(!relocated.has(match[1]), `duplicate target path ${match[1]}`);
    relocated.add(match[1]);
    return `${match[1]}${match[2]}${slot}${match[3].slice(source.length)}`;
  }).join('\n');
  assert.equal(relocated.size, pathKeys.size, 'all target paths must be relocated');
  assert.ok(!text.includes(source), 'restored configuration still references source');
  await writeFile(join(slot, 'app.ini'), text, { mode: 0o600 });
  assert.deepEqual(await storedIdentity(slot), expectedIdentity, 'target configuration changed recovered identity');
  emit('acceptance', { stage: slot.split('/').at(-1), feature: 'recovered-stored-secrets-and-ssh-keys', outcome: 'passed', identitySha256: hash(JSON.stringify(expectedIdentity)) });
}
async function hostIdentity(slot, expected) {
  const publicKey = await readFile(join(slot, 'data/ssh/forgejo.ed25519.pub'), 'utf8');
  if (expected) assert.equal(publicKey, expected, 'SSH host identity changed');
  await writeFile(join(work, 'known_hosts'), `[127.0.0.1]:${sshPort} ${publicKey.trim()}\n`);
  emit('ssh-host-identity', { sha256: hash(publicKey), fingerprint: await command('ssh-keygen', ['-lf', join(slot, 'data/ssh/forgejo.ed25519.pub')]) });
  return publicKey;
}
const repoPath = '/api/v1/repos/proof-admin/proof';
async function gitTransport(stage, create = false) {
  const client = join(work, `client-${stage}`);
  const httpEnv = { GIT_CONFIG_COUNT: '1', GIT_CONFIG_KEY_0: 'http.extraHeader', GIT_CONFIG_VALUE_0: `Authorization: ${basic}` };
  await command('git', ['clone', `${base}/proof-admin/proof.git`, client], { env: httpEnv });
  await command('git', ['lfs', 'install', '--local'], { cwd: client });
  if (create) {
    await command('git', ['checkout', '-b', 'main'], { cwd: client });
    await command('git', ['lfs', 'track', '*.bin'], { cwd: client });
    await writeFile(join(client, 'payload.bin'), lfsBytes);
    await writeFile(join(client, 'README.md'), 'Native proof initial state\n');
  } else {
    await command('git', ['lfs', 'pull'], { cwd: client, env: httpEnv });
    assert.equal(await readFile(join(client, 'README.md'), 'utf8'), 'Native proof initial state\n');
    assert.deepEqual(await readFile(join(client, 'payload.bin')), lfsBytes, 'LFS object reconstruction');
    assert.equal(await command('git', ['rev-parse', 'HEAD'], { cwd: client }), await readFile(join(work, 'initial-head'), 'utf8'));
  }
  await writeFile(join(client, `${stage}.txt`), `${stage} useful new work\n`);
  await command('git', ['add', '.'], { cwd: client });
  await command('git', ['commit', '-m', `Exercise ${stage}`], { cwd: client });
  await command('git', ['push', '-u', 'origin', 'main'], { cwd: client, env: httpEnv });
  if (create) await writeFile(join(work, 'initial-head'), await command('git', ['rev-parse', 'HEAD'], { cwd: client }));
  const sshClient = join(work, `ssh-client-${stage}`);
  const sshEnv = { GIT_SSH_COMMAND: `ssh -i ${join(work, 'client-key')} -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=${join(work, 'known_hosts')}`, GIT_LFS_SKIP_SMUDGE: '1' };
  await command('git', ['clone', `ssh://git@127.0.0.1:${sshPort}/proof-admin/proof.git`, sshClient], { env: sshEnv });
  // LFS was exercised over authenticated HTTP above; this commit changes only text.
  await writeFile(join(sshClient, `ssh-${stage}.txt`), 'SSH write accepted\n');
  await command('git', ['add', '.'], { cwd: sshClient });
  await command('git', ['commit', '-m', `Exercise SSH ${stage}`], { cwd: sshClient });
  await command('git', ['push', 'origin', 'main'], { cwd: sshClient, env: sshEnv });
  if (create) await writeFile(join(work, 'initial-head'), await command('git', ['rev-parse', 'HEAD'], { cwd: sshClient }));
  emit('acceptance', { stage, feature: 'http-and-ssh-clone-push-and-lfs', outcome: 'passed' });
}
async function seed() {
  await request('/api/v1/admin/users', { method: 'POST', expected: [201], body: { username: 'proof-member', email: 'proof-member@example.invalid', password, must_change_password: false } });
  await request('/api/v1/user/keys', { method: 'POST', expected: [201], body: { title: 'native-proof', key: await readFile(join(work, 'client-key.pub'), 'utf8') } });
  await request('/api/v1/user/repos', { method: 'POST', expected: [201], body: { name: 'proof', private: true, default_branch: 'main', auto_init: false } });
  await gitTransport('source', true);
  await request(`${repoPath}/issues`, { method: 'POST', expected: [201], body: { title: 'Preserved issue', body: 'Native recovery fixture', assignees: ['proof-admin'] } });
  await request(`${repoPath}/issues/1/comments`, { method: 'POST', expected: [201], body: { body: 'Preserved comment' } });
  const form = new FormData();
  form.append('attachment', new Blob([fixtureBytes], { type: 'text/plain' }), 'proof.txt');
  const attachment = await request(`${repoPath}/issues/1/assets`, { method: 'POST', expected: [201], body: form });
  await writeFile(join(work, 'attachment.json'), JSON.stringify(attachment));
  await request('/api/packages/proof-admin/generic/native-proof/1.0.0/proof.txt', { method: 'PUT', expected: [201], body: fixtureBytes, headers: { 'Content-Type': 'application/octet-stream' } });
  await request(`${repoPath}/releases`, { method: 'POST', expected: [201], body: { tag_name: 'v1.0.0', target_commitish: 'main', name: 'Preserved release', body: 'Native recovery fixture' } });
}
async function verify(stage) {
  assert.equal((await request('/api/v1/users/proof-member')).login, 'proof-member');
  assert.equal((await request(`${repoPath}/issues/1`)).title, 'Preserved issue');
  assert.equal((await request(`${repoPath}/issues/1/comments`))[0].body, 'Preserved comment');
  const attachment = JSON.parse(await readFile(join(work, 'attachment.json'), 'utf8'));
  const assets = await request(`${repoPath}/issues/1/assets`);
  assert.ok(assets.some(item => item.uuid === attachment.uuid), 'attachment metadata preserved');
  const attachmentURL = new URL(attachment.browser_download_url, base);
  assert.equal(attachmentURL.origin, base, 'fixture must not contact external services');
  assert.deepEqual(await request(attachmentURL.pathname, { raw: true }), fixtureBytes);
  assert.deepEqual(await request('/api/packages/proof-admin/generic/native-proof/1.0.0/proof.txt', { raw: true }), fixtureBytes);
  assert.equal((await request(`${repoPath}/releases/tags/v1.0.0`)).name, 'Preserved release');
  await gitTransport(stage);
  await request(`${repoPath}/issues`, { method: 'POST', expected: [201], body: { title: `${stage} new work`, body: 'Post-reconstruction write' } });
  await request(`/api/packages/proof-admin/generic/native-proof/${stage}/proof.txt`, { method: 'PUT', expected: [201], body: fixtureBytes, headers: { 'Content-Type': 'application/octet-stream' } });
  emit('acceptance', { stage, feature: 'users-issues-comments-attachments-releases-packages-and-new-work', outcome: 'passed' });
}
async function main() {
  assert.equal(process.platform, 'linux', 'remote Linux only');
  assert.equal(process.env.BARTL_FORGEJO_LAB, 'hetzner', 'explicit Hetzner lab opt-in required');
  assert.notEqual(process.getuid(), 0, 'Forgejo must run as an unprivileged user');
  assert.equal(process.arch, 'x64', 'pinned artifacts support linux-amd64 only');
  assert.ok(Number(process.versions.node.split('.')[0]) >= 22, 'Node.js >=22 required');
  assert.ok(process.argv[2]?.startsWith('/'), 'absolute caller-supplied workdir required');
  assert.ok(!/\s/.test(work), 'workdir must not contain whitespace (SSH command path)');
  for (const port of [httpPort, sshPort]) assert.ok(Number.isInteger(port) && port > 1024 && port < 65536, 'unprivileged loopback ports required');
  assert.notEqual(httpPort, sshPort);
  try { assert.equal((await readdir(work)).length, 0, 'workdir must be empty'); }
  catch (error) { if (error.code !== 'ENOENT') throw error; }
  await mkdir(work, { recursive: true, mode: 0o700 });
  await chmod(work, 0o700);
  await mkdir(join(work, 'raw'));
  await mkdir(join(work, 'bin'));
  await mkdir(join(work, 'home'));
  process.env.HOME = join(work, 'home');
  process.env.TMPDIR = join(work, 'tmp');
  process.env.XDG_CACHE_HOME = join(work, 'cache');
  await mkdir(process.env.TMPDIR);
  for (const tool of ['curl', 'git', 'git-lfs', 'ssh', 'ssh-keygen', 'tar', 'sha256sum']) {
    const probe = spawnSync('sh', ['-c', 'command -v "$1"', 'sh', tool], { encoding: 'utf8' });
    assert.equal(probe.status, 0, `missing prerequisite: ${tool}; script installs nothing`);
  }
  await command('git', ['lfs', 'version']);
  for (const [version, checksum] of Object.entries(releases)) {
    const url = `https://codeberg.org/forgejo/forgejo/releases/download/v${version}/forgejo-${version}-linux-amd64`;
    await command('curl', ['--fail', '--location', '--retry', '3', '--output', binary(version), url]);
    assert.equal(hash(await readFile(binary(version))), checksum, `pinned ${version} checksum mismatch`);
    await chmod(binary(version), 0o700);
    emit('artifact', { kind: 'upstream-binary', version, url, sha256: checksum });
  }
  await command('ssh-keygen', ['-q', '-t', 'ed25519', '-N', '', '-f', join(work, 'client-key')]);
  await config(source);
  await mkdir(join(source, 'data/ssh'), { recursive: true });
  await command('ssh-keygen', ['-q', '-t', 'ed25519', '-N', '', '-f', join(source, 'data/ssh/forgejo.ed25519')]);
  await cli(source, '16.0.7', ['migrate']);
  await cli(source, '16.0.7', ['admin', 'user', 'create', '--username', 'proof-admin', '--password', password, '--email', 'proof-admin@example.invalid', '--admin', '--must-change-password=false'], { sensitive: true });
  token = await cli(source, '16.0.7', ['admin', 'user', 'generate-access-token', '--username', 'proof-admin', '--token-name', 'native-proof', '--scopes', 'all', '--raw'], { sensitive: true });
  assert.ok(/^[a-f0-9]{40}$/i.test(token), 'native token output must be unambiguous; inspect private raw logs');
  await writeFile(join(work, 'fixture-access.json'), JSON.stringify({
    username: 'proof-admin', password, token, privateClientKey: join(work, 'client-key'),
    httpPort, sshPort,
  }), { mode: 0o600 });
  const hostKey = await hostIdentity(source);
  await start(source, '16.0.7');
  await seed();
  await stop();
  const sourceDigest = await treeDigest(source);
  const sourceIdentity = await storedIdentity(source);
  const point = await capture(source, 'source-16.0.7');
  // Restart uses original owned data and never accesses the recovery point.
  await start(source, '16.0.7');
  await request(`${repoPath}/issues/1`);
  await hostIdentity(source, hostKey);
  await stop();
  const protectedDigest = await treeDigest(source);
  emit('acceptance', { stage: 'source-restart', feature: 'existing-state-and-identity', outcome: 'passed' });
  // A digest catches writes, not read-only dependencies on the old installation.
  // Remove its original path while recovered slots exercise all native features.
  await rename(source, protectedSource);
  sourceParked = true;
  for (const [stage, version] of [['same-version', '16.0.7'], ['upgrade', '17.0.1']]) {
    const slot = join(work, stage);
    await assert.rejects(lstat(source), { code: 'ENOENT' }, 'source path must be unavailable during recovery');
    assert.equal(hash(await readFile(point)), await readFile(`${point}.sha256`, 'utf8').then(text => text.split(' ')[0]), 'capture integrity');
    await restore(point, slot, sourceIdentity);
    await hostIdentity(slot, hostKey);
    if (stage === 'upgrade') {
      // Upstream upgrade guide requires flushing serialized queues with the old
      // release. This happens only on this isolated recovered slot, never source.
      await start(slot, '16.0.7');
      await cli(slot, '16.0.7', ['manager', 'flush-queues', '--timeout', '60s']);
      await stop();
    }
    await start(slot, version); // Native startup, not a custom migration subsystem.
    await verify(stage);
    await hostIdentity(slot, hostKey);
    await stop();
    assert.deepEqual(await storedIdentity(slot), sourceIdentity, 'native startup or new work changed recovered identity');
    await capture(slot, `${stage}-${version}`);
    await assert.rejects(lstat(source), { code: 'ENOENT' }, 'recovery recreated the original source path');
    assert.equal(await treeDigest(protectedSource), protectedDigest, 'restore/upgrade changed original source');
    emit('acceptance', { stage, feature: 'original-source-unchanged', outcome: 'passed', sourceTreeSha256: protectedDigest });
    emit('acceptance', { stage, feature: 'original-source-path-unavailable', outcome: 'passed' });
  }
  await rename(protectedSource, source);
  sourceParked = false;
  emit('scope', { unsupported: ['external ingress and TLS (separate harness acceptance)', 'production activation or provider-loss fencing', 'Actions execution, federation, email, mirrors, external webhooks'], note: 'Loopback fixture deliberately disables external effects; this proof does not qualify the complete application contract.' });
  emit('result', { outcome: 'passed', workdir: work, initialSourceTreeSha256: sourceDigest, protectedSourceTreeSha256: protectedDigest });
}
try { await main(); }
catch (error) {
  emit('result', { outcome: 'failed', reason: error.message, workdir: work });
  process.exitCode = 1;
} finally {
  if (running) {
    try { await stop(); }
    catch (error) {
      emit('cleanup-failed', { reason: error.message, pid: running?.pid });
      // A failed graceful stop never qualifies a capture. On proof failure only,
      // force the isolated lab child down rather than leave a background writer.
      if (running?.exitCode === null && running?.signalCode === null) {
        const child = running;
        child.kill('SIGKILL');
        await new Promise(resolveExit => child.once('exit', resolveExit));
        emit('cleanup-forced-stop', { pid: child.pid });
      }
      process.exitCode = 1;
    }
  }
  if (sourceParked) {
    try {
      await rename(protectedSource, source);
      sourceParked = false;
    } catch (error) {
      emit('cleanup-failed', { reason: `Could not restore original source path: ${error.message}`, preservedSource: protectedSource });
      process.exitCode = 1;
    }
  }
}
