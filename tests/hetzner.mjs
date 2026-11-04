#!/usr/bin/env node
// SPDX-License-Identifier: Apache-2.0
// Local orchestration only. Cloud credentials never enter cloud-init or SSH.
import { promises as fs } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { randomUUID } from 'node:crypto';
import { execFileSync } from 'node:child_process';

const NAME = 'bartl-forgejo-lab';
const MANAGER = 'bartl-forgejo-lab';
const HOURS = 12;
const LIMIT = 2;
const RETENTION = 'ghcr.io/bartlapp';
const checkout = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const stateDir = path.resolve(process.env.LAB_STATE_DIR || path.join(os.homedir(), '.local/state/bartl-forgejo-lab'));
const stateFile = path.join(stateDir, 'lab.json');
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const [command, ...args] = process.argv.slice(2);

function options() {
  const result = {};
  for (let n = 0; n < args.length; n += 2) {
    if (!['--retention', '--cleanup-executor'].includes(args[n]) || !args[n + 1] || result[args[n]]) {
      throw new Error('Expected --retention ghcr.io/bartlapp --cleanup-executor parent');
    }
    result[args[n]] = args[n + 1];
  }
  return result;
}

async function prepareState() {
  if (stateDir === checkout || stateDir.startsWith(checkout + path.sep)) {
    throw new Error('LAB_STATE_DIR must be outside the checkout');
  }
  await fs.mkdir(stateDir, { recursive: true, mode: 0o700 });
  const stat = await fs.lstat(stateDir);
  if (!stat.isDirectory() || stat.isSymbolicLink() || stat.uid !== process.getuid()) {
    throw new Error('State directory must be a real directory owned by the current user');
  }
  await fs.chmod(stateDir, 0o700);
  const real = await fs.realpath(stateDir);
  const realCheckout = await fs.realpath(checkout);
  if (real === realCheckout || real.startsWith(realCheckout + path.sep)) {
    throw new Error('Resolved LAB_STATE_DIR must be outside the checkout');
  }
}

async function load() {
  try {
    const stat = await fs.lstat(stateFile);
    if (!stat.isFile() || stat.isSymbolicLink() || stat.uid !== process.getuid() || (stat.mode & 0o077)) {
      throw new Error('State file must be owned by the current user with mode 0600');
    }
    const state = JSON.parse(await fs.readFile(stateFile, 'utf8'));
    if (state.version !== 1 || !/^[a-f0-9-]{36}$/.test(state.owner)) throw new Error('Invalid lab state');
    return state;
  } catch (error) {
    if (error.code === 'ENOENT') return null;
    throw error;
  }
}

async function save(state) {
  const temporary = stateFile + '.' + randomUUID();
  const handle = await fs.open(temporary, 'wx', 0o600);
  try { await handle.writeFile(JSON.stringify(state, null, 2) + '\n'); await handle.sync(); }
  finally { await handle.close(); }
  await fs.rename(temporary, stateFile);
}

async function locked(work) {
  const lock = path.join(stateDir, 'lock');
  try { await fs.mkdir(lock, { mode: 0o700 }); }
  catch (error) {
    if (error.code === 'EEXIST') throw new Error('Lab mutation locked. Inspect the active process before manually removing a stale state-directory lock.');
    throw error;
  }
  try { return await work(); }
  finally { await fs.rmdir(lock); }
}

async function api(method, endpoint, body, allow404 = false) {
  const token = process.env.HCLOUD_TOKEN;
  if (!token) throw new Error('HCLOUD_TOKEN is required in the local environment');
  const response = await fetch('https://api.hetzner.cloud/v1' + endpoint, {
    method,
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    ...(body ? { body: JSON.stringify(body) } : {}),
    signal: AbortSignal.timeout(60000),
  });
  if (response.status === 404 && allow404) return null;
  if (!response.ok) {
    // Never log request headers, request bodies, or provider response bodies.
    throw new Error(`Hetzner ${method} ${endpoint.split('?')[0]} returned HTTP ${response.status}`);
  }
  return response.status === 204 ? {} : response.json();
}

async function list(collection) {
  const values = [];
  for (let page = 1; ; page++) {
    const response = await api('GET', `/${collection}?per_page=50&page=${page}`);
    values.push(...response[collection]);
    if (!response.meta?.pagination?.next_page) return values;
  }
}

function owned(resource, state) {
  return !!state && resource.name === NAME && resource.labels?.['managed-by'] === MANAGER && resource.labels?.['lab-owner'] === state.owner;
}

async function inventory() {
  const [servers, primary_ips, volumes, floating_ips] = await Promise.all(
    ['servers', 'primary_ips', 'volumes', 'floating_ips'].map(list),
  );
  return { servers, primary_ips, volumes, floating_ips };
}

function assertExclusive(resources, state) {
  if (resources.volumes.length || resources.floating_ips.length ||
      resources.servers.some(server => !owned(server, state)) ||
      resources.primary_ips.some(ip => !owned(ip, state)) || resources.servers.length > 1 ||
      resources.primary_ips.length > 1) {
    throw new Error('Refusing provision: existing foreign resources or conflicting lab resources in project');
  }
}

async function selectOffer() {
  const [prices, types, locations] = await Promise.all([
    api('GET', '/pricing'), list('server_types'), list('locations'),
  ]);
  const offers = [];
  for (const type of types) {
    if (type.architecture !== 'x86' || type.memory < 4) continue;
    const pricing = prices.pricing.server_types.find(item => item.id === type.id);
    for (const price of pricing?.prices || []) {
      const location = locations.find(item => item.name === price.location);
      // Availability and deprecation are per location, not per datacenter or type.
      // https://docs.hetzner.cloud/changelog#2026-04-01-datacenter-deprecations
      const availability = type.locations?.find(item => item.id === location?.id);
      if (!location || availability?.available !== true || availability.deprecation) continue;
      const ipPrice = prices.pricing.primary_ips?.find(item => item.type === 'ipv4')?.prices?.find(item => item.location === price.location);
      const serverHourly = Number(price.price_hourly?.gross);
      const ipv4Hourly = Number(ipPrice?.price_hourly?.gross);
      if (!Number.isFinite(serverHourly) || !Number.isFinite(ipv4Hourly) || serverHourly <= 0 || ipv4Hourly <= 0) continue;
      // A full additional billing hour and 10% margin leave time for API deletion.
      const bound = (serverHourly + ipv4Hourly) * (HOURS + 1) * 1.1;
      if (bound <= LIMIT) offers.push({ type: type.name, location: location.name, memoryGB: type.memory, serverHourly, ipv4Hourly, grossBoundEUR: bound });
    }
  }
  offers.sort((a, b) => a.grossBoundEUR - b.grossBoundEUR || a.type.localeCompare(b.type));
  if (!offers.length) throw new Error('No currently available x86 >=4GB offer with explicit gross IPv4 pricing fits EUR2 / 12h');
  if (prices.pricing.currency !== 'EUR') throw new Error('Hetzner pricing currency must be EUR');
  return offers[0];
}

async function makeKeys(state) {
  const clientKey = path.join(stateDir, `client_${state.owner}_ed25519`);
  const hostKey = path.join(stateDir, `host_${state.owner}_ed25519`);
  for (const key of [clientKey, hostKey]) {
    try {
      const stat = await fs.lstat(key);
      if (!stat.isFile() || stat.isSymbolicLink() || stat.uid !== process.getuid() || (stat.mode & 0o077)) throw new Error('Unsafe SSH private key permissions');
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
      execFileSync('ssh-keygen', ['-q', '-t', 'ed25519', '-N', '', '-C', NAME, '-f', key], { stdio: 'pipe' });
      await fs.chmod(key, 0o600);
    }
  }
  state.clientKey = clientKey;
  state.hostKey = hostKey;
  return {
    client: (await fs.readFile(clientKey + '.pub', 'utf8')).trim(),
    hostPublic: (await fs.readFile(hostKey + '.pub', 'utf8')).trim(),
    hostPrivate: await fs.readFile(hostKey, 'utf8'),
  };
}

function cloudInit(keys) {
  // JSON is a YAML subset. Only ephemeral SSH material goes to this server.
  return '#cloud-config\n' + JSON.stringify({
    ssh_pwauth: false,
    disable_root: false,
    ssh_deletekeys: true,
    ssh_keys: { ed25519_private: keys.hostPrivate, ed25519_public: keys.hostPublic },
    ssh_authorized_keys: [keys.client],
    users: ['default', {
      name: 'runner', groups: ['sudo'], shell: '/bin/bash', lock_passwd: true,
      sudo: ['ALL=(ALL) NOPASSWD:ALL'], ssh_authorized_keys: [keys.client],
    }],
    package_update: true,
    packages: ['git', 'git-lfs', 'curl', 'jq', 'sqlite3', 'openssh-server', 'nodejs', 'ca-certificates', 'xz-utils', 'tar', 'coreutils', 'podman', 'skopeo', 'uidmap'],
    write_files: [{
      path: '/etc/ssh/sshd_config.d/00-bartl-lab.conf', owner: 'root:root', permissions: '0644',
      content: 'PasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin prohibit-password\nHostKey /etc/ssh/ssh_host_ed25519_key\n',
    }, {
      path: '/usr/local/sbin/bartl-lab-node', owner: 'root:root', permissions: '0700',
      content: `#!/bin/sh
set -eu
work=$(mktemp -d /var/tmp/bartl-node.XXXXXX)
trap 'rm -rf "$work"' EXIT
cd "$work"
base=https://nodejs.org/dist/latest-v22.x
curl --fail --silent --show-error --location --max-time 120 --retry 2 "$base/SHASUMS256.txt" -o SHASUMS256.txt
artifact=$(awk '$2 ~ /^node-v22\\.[0-9]+\\.[0-9]+-linux-x64\\.tar\\.xz$/ {print $2}' SHASUMS256.txt)
[ "$(printf '%s\\n' "$artifact" | wc -l)" -eq 1 ]
[ -n "$artifact" ]
curl --fail --silent --show-error --location --max-time 300 --retry 2 "$base/$artifact" -o "$artifact"
awk -v file="$artifact" '$2 == file' SHASUMS256.txt | sha256sum -c -
mkdir -p /opt/node22
tar -xJf "$artifact" --strip-components=1 -C /opt/node22
for executable in node npm npx; do ln -sf "/opt/node22/bin/$executable" "/usr/local/bin/$executable"; done
/usr/local/bin/node --version
`,
    }],
    runcmd: [['systemctl', 'restart', 'ssh'], ['/usr/local/sbin/bartl-lab-node']],
  });
}

async function connection(state, server) {
  const ip = server.public_net?.ipv4?.ip;
  if (!ip) throw new Error('Server has no IPv4 address yet; inspect again');
  const hostPublic = (await fs.readFile(state.hostKey + '.pub', 'utf8')).trim();
  const knownHosts = path.join(stateDir, 'known_hosts');
  await fs.writeFile(knownHosts, `${ip} ${hostPublic}\n`, { mode: 0o600 });
  const sshConfig = path.join(stateDir, 'ssh_config');
  const quote = value => '"' + value.replaceAll('\\', '\\\\').replaceAll('"', '\\"') + '"';
  await fs.writeFile(sshConfig, `Host bartl-forgejo-lab\n  HostName ${ip}\n  User runner\n  IdentityFile ${quote(state.clientKey)}\n  UserKnownHostsFile ${quote(knownHosts)}\n  StrictHostKeyChecking yes\n  IdentitiesOnly yes\n  PasswordAuthentication no\n  BatchMode yes\n`, { mode: 0o600 });
  state.ip = ip;
  state.sshConfig = sshConfig;
}

function summary(state, resources) {
  return {
    stateDirectory: stateDir,
    deadline: state?.deadline || null,
    retention: state?.retention || null,
    cost: state?.offer || null,
    sshConfig: state?.sshConfig || null,
    cleanup: 'Independent local watchdog must remain running with HCLOUD_TOKEN. Shutdown does not stop billing.',
    servers: resources.servers.map(s => ({ id: s.id, name: s.name, status: s.status, owned: owned(s, state), ipv4: s.public_net?.ipv4?.ip })),
    primaryIPs: resources.primary_ips.map(ip => ({ id: ip.id, name: ip.name, owned: owned(ip, state), autoDelete: ip.auto_delete })),
    volumes: resources.volumes.map(v => ({ id: v.id, name: v.name })),
    floatingIPs: resources.floating_ips.map(ip => ({ id: ip.id, name: ip.name })),
  };
}

async function provision(opts) {
  if (opts['--retention'] !== RETENTION || opts['--cleanup-executor'] !== 'parent') {
    throw new Error('Provision requires --retention ghcr.io/bartlapp --cleanup-executor parent');
  }
  let state = await load();
  const resources = await inventory();
  assertExclusive(resources, state);
  if (resources.servers.length) {
    if (Date.now() >= Date.parse(state.deadline)) throw new Error('Existing lab deadline expired; destroy it');
    const server = resources.servers[0];
    if (state.serverId && state.serverId !== server.id) throw new Error('Server identity conflict');
    state.serverId = server.id;
    state.primaryIpId = server.public_net.ipv4.id;
    await connection(state, server);
    await save(state);
    console.log(JSON.stringify(summary(state, resources), null, 2));
    return;
  }
  if (resources.primary_ips.length || state?.serverId || state?.primaryIpId) throw new Error('Previous lab is not fully destroyed; run destroy before provision');
  if (state?.deadline) throw new Error('Previous lab intent exists; run destroy to close it before creating a new lab');
  const offer = await selectOffer();
  console.log(`Current gross price: server EUR${offer.serverHourly}/h + IPv4 EUR${offer.ipv4Hourly}/h. Conservative ${HOURS}h allocation including one extra hour and 10% margin: EUR${offer.grossBoundEUR.toFixed(4)} <= EUR${LIMIT}; total authorization EUR10.`);
  console.log('Retention destination: ghcr.io/bartlapp. No cloud backups or volumes. Parent owns deletion; a powered-off server still bills.');
  state = { version: 1, owner: randomUUID(), deadline: new Date(Date.now() + HOURS * 3600000).toISOString(), retention: RETENTION, offer };
  const keys = await makeKeys(state);
  await save(state); // Persist identity and deletion deadline before a possibly ambiguous create.
  const labels = { 'managed-by': MANAGER, 'lab-owner': state.owner };
  const createdIP = await api('POST', '/primary_ips', {
    name: NAME, type: 'ipv4', assignee_type: 'server', location: offer.location,
    auto_delete: true, labels,
  });
  state.primaryIpId = createdIP.primary_ip.id;
  await save(state);
  const response = await api('POST', '/servers', {
    name: NAME, server_type: offer.type, location: offer.location, image: 'ubuntu-24.04',
    start_after_create: true, automount: false, labels,
    public_net: { enable_ipv4: true, enable_ipv6: false, ipv4: state.primaryIpId },
    user_data: cloudInit(keys),
  });
  state.serverId = response.server.id;
  await save(state);
  await connection(state, response.server);
  await save(state);
  console.log(JSON.stringify(summary(state, await inventory()), null, 2));
}

async function absent(collection, id) {
  for (let attempt = 0; attempt < 120; attempt++) {
    if (await api('GET', `/${collection}/${id}`, undefined, true) === null) {
      console.log(`Verified GET /${collection}/${id}: HTTP 404`);
      return;
    }
    await sleep(1000);
  }
  throw new Error(`Deletion not confirmed for ${collection}/${id}; rerun destroy`);
}

async function removeOwned(collection, id, state) {
  const response = await api('GET', `/${collection}/${id}`, undefined, true);
  if (response === null) return absent(collection, id);
  const resource = response[collection === 'servers' ? 'server' : 'primary_ip'];
  if (!owned(resource, state)) throw new Error(`Refusing to delete unowned ${collection}/${id}`);
  if (resource.protection?.delete) throw new Error(`Refusing protected ${collection}/${id}`);
  await api('DELETE', `/${collection}/${id}`, undefined, true);
  await absent(collection, id);
}

async function destroy() {
  const state = await load();
  const resources = await inventory();
  if (!state) {
    if (resources.servers.some(s => s.name === NAME) || resources.primary_ips.some(ip => ip.name === NAME)) {
      throw new Error('Lab-named resources exist without local ownership state; refusing deletion');
    }
    console.log('No locally owned lab intent or resources');
    return;
  }
  const servers = resources.servers.filter(s => owned(s, state));
  const ips = resources.primary_ips.filter(ip => owned(ip, state));
  const serverIds = new Set([...servers.map(s => s.id), ...(state.serverId ? [state.serverId] : [])]);
  const ipIds = new Set([...ips.map(ip => ip.id), ...servers.map(s => s.public_net?.ipv4?.id).filter(Boolean), ...(state.primaryIpId ? [state.primaryIpId] : [])]);
  // Save discovered identities before deletion, including a create whose response was lost.
  state.deletingServerIds = [...new Set([...serverIds, ...(state.deletingServerIds || [])])];
  state.deletingIpIds = [...new Set([...ipIds, ...(state.deletingIpIds || [])])];
  await save(state);
  for (const id of state.deletingServerIds) await removeOwned('servers', id, state);
  for (const id of state.deletingIpIds) await removeOwned('primary_ips', id, state);
  const remaining = await inventory();
  if (remaining.servers.some(s => owned(s, state)) || remaining.primary_ips.some(ip => owned(ip, state))) throw new Error('Owned resources remain after deletion');
  await save({ version: 1, owner: state.owner, lastDestroyed: new Date().toISOString() });
  console.log('Owned server and primary IPv4 deletion verified; foreign resources were not changed. Ephemeral SSH keys remain in the protected state directory.');
}

async function watchdog() {
  if (!process.env.HCLOUD_TOKEN) throw new Error('HCLOUD_TOKEN is required in the local environment');
  const initial = await load();
  if (!initial?.deadline) { console.log('No outstanding lab deadline'); return; }
  console.log('Deletion watchdog active locally; credentials stay in environment. Powered-off servers continue billing.');
  for (;;) {
    try {
      const state = await load();
      if (!state?.deadline || state.owner !== initial.owner) { console.log('Watched lab intent closed or replaced'); return; }
      const deadline = Date.parse(state.deadline);
      if (!Number.isFinite(deadline)) throw new Error('Invalid recorded deadline');
      const resources = await inventory();
      const own = [...resources.servers, ...resources.primary_ips].some(r => owned(r, state));
      if ((!own && state.serverId) || Date.now() >= deadline) {
        await locked(async () => {
          if ((await load())?.owner === initial.owner) await destroy();
        });
        return;
      }
      await sleep(Math.min(30000, deadline - Date.now()));
    } catch (error) {
      // A network outage or another local operation must not silently disarm deletion.
      console.error(`Deletion watchdog: ${error.message}; retrying in 30s`);
      await sleep(30000);
    }
  }
}

try {
  if (!['inspect', 'provision', 'destroy', 'watchdog'].includes(command)) {
    throw new Error('Usage: node tests/hetzner.mjs inspect|destroy|watchdog; provision --retention ghcr.io/bartlapp --cleanup-executor parent');
  }
  const opts = options();
  if (command !== 'provision' && args.length) throw new Error('Only provision accepts arguments');
  await prepareState();
  if (command === 'provision') await locked(() => provision(opts));
  else if (command === 'destroy') await locked(destroy);
  else if (command === 'watchdog') await watchdog();
  else console.log(JSON.stringify(summary(await load(), await inventory()), null, 2));
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
