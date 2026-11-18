// Source-direct crane-dev deployment (deploy/scripts/local-dev-deploy.mjs) with a fake Docker:
// full-SHA local tags, no overwrite of an existing SHA with different content, no pull, running
// image ID verified, only crane-dev; R8 closure: explicit GOPROXY build argument, the running R6
// registry release captured as a local baseline before the first local apply (rollback without any
// registry), current.json only after image IDs + schema + smoke, failed apply restored and recorded,
// single-instance lock, maintenance and Fleet/ACL updates through the ACTIVE override.
import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, existsSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { build, plan, apply, rollback, localTag, overrideYaml, validateLocalDevelopment, changedServices, BUILDABLE, buildGoproxy, composeMaintenance, updateFleet, ACTIVE_OVERRIDE, resetBusiness, SIM_CONTAINER, SIM_SPOOL_VOLUME, readFleet } from '../../deploy/scripts/local-dev-deploy.mjs';
import { validateProduction } from '../../tools/production-contract.mjs';

const UP = JSON.parse(readFileSync('deploy/production/images.json', 'utf8'));
const R6 = 'a2a89ea' + '0'.repeat(33);
const REG = { 'cloud-api': `ghcr.io/guolichen007/crane-operations-platform/dev/cloud-api@sha256:${'c'.repeat(64)}`, 'web-console': `ghcr.io/guolichen007/crane-operations-platform/dev/web-console@sha256:${'d'.repeat(64)}` };
const fleetDoc = (ids) => JSON.stringify({ tenantId: 'demo', devices: ids.map((id) => ({ runtimeDeviceId: id, projectId: 'P1', assetDeviceId: 'A-' + id, sourceMode: 'SIMULATION' })) });
const aclDoc = (ids) => [...ids, 'cloud-api'].map((u) => `user ${u}\ntopic read crane/v1/demo/P1/${u}/heartbeat\n`).join('');

function repo(t) {
  const dir = mkdtempSync(join(tmpdir(), 'crane-local-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const root = join(dir, 'checkout'); mkdirSync(root);
  const git = (...a) => { const r = spawnSync('git', ['-C', root, '-c', 'user.name=A', '-c', 'user.email=a@example.test', '-c', 'commit.gpgsign=false', ...a], { encoding: 'utf8' }); assert.equal(r.status, 0, r.stderr); return r.stdout.trim(); };
  git('init', '-q');
  for (const f of ['deploy/compose/docker-compose.prod.yml', 'deploy/compose/docker-compose.crane-dev.yml', 'deploy/postgres/migrations/00012_x.sql', 'apps/web-console/a.ts', 'internal/a.go', 'go.mod']) { mkdirSync(join(root, f, '..'), { recursive: true }); writeFileSync(join(root, f), 'x\n'); }
  git('add', '.'); git('commit', '-qm', 'one');
  const fleet = join(dir, 'fleet.json'), acl = join(dir, 'acl');
  writeFileSync(fleet, fleetDoc(['SIM-OLD']), { mode: 0o600 }); writeFileSync(acl, aclDoc(['SIM-OLD']), { mode: 0o600 });
  const inventory = join(dir, 'inventory.json'); writeFileSync(inventory, JSON.stringify({ tenantId: 'demo', projects: [{ id: 'P001' }], assetDevices: [], runtimeDevices: [] }), { mode: 0o600 });
  const env = join(dir, 'crane.env'); writeFileSync(env, `CRANE_LOCAL_DEPLOY_DIR=${join(dir, 'state')}\nCRANE_FLEET_FILE=${fleet}\nCRANE_MQTT_ACL=${acl}\nCRANE_INVENTORY_FILE=${inventory}\n`, { mode: 0o600 });
  return { dir, root, git, env, fleet, acl, inventory, state: join(dir, 'state'), sha: () => git('rev-parse', 'HEAD') };
}

function devConfig(images) {
  const services = {};
  for (const name of ['postgres', 'keycloak', 'mosquitto', 'cloud-api', 'web-console'])
    services[name] = { image: UP[name] ?? 'x@sha256:' + 'a'.repeat(64), logging: { driver: 'json-file', options: { 'max-size': '10m', 'max-file': '5' } }, networks: { backend: null } };
  for (const s of ['cloud-api', 'web-console']) { services[s].image = images[s]; services[s].pull_policy = 'never'; }
  services['cloud-api'].environment = { APP_ENV: 'production', OIDC_CLIENT_SECRET_FILE: '/run/secrets/oidc_client_secret', OIDC_ISSUER: 'https://crane.youdaozd.com/realms/crane', OIDC_CLIENT_ID: 'crane-bff', PUBLIC_URL: 'https://crane.youdaozd.com', CRANE_DEPLOYMENT_ENVIRONMENT: 'crane-dev' };
  services.keycloak.environment = { KC_HOSTNAME_STRICT: 'true', KC_DB: 'postgres', KC_HOSTNAME: 'https://crane.youdaozd.com' };
  services.keycloak.volumes = [{ target: '/opt/keycloak/themes/u-dao', source: '/a', read_only: true }];
  services['web-console'].networks.frontend = null; services.keycloak.networks.frontend = null;
  return { name: 'u-dao-crane-ops', services, networks: { frontend: { external: true, name: 'u-dao-crane-frontend' }, backend: { internal: true } }, secrets: Object.fromEntries(Array.from({ length: 6 }, (_, i) => ['s' + i, { file: '/a/' + i }])) };
}

/**
 * Fake docker. Images keyed by reference (tag or registry digest ref); containers run a reference.
 * The site starts on the R6 registry release (REG). `up` makes the containers run what the given
 * override names (or, without a local override, the registry refs). Records every call.
 */
function fakeDocker(r, { buildId = (tag) => 'sha256:' + Buffer.from(tag).toString('hex').padEnd(64, '0').slice(0, 64), arch = 'amd64', smokeOk = () => true } = {}) {
  const state = { images: {}, running: { ...REG }, calls: [], schema: 12, lastOverride: null, upCount: 0, failUp: null, hup: 0, resetCalls: [], businessRows: 2, simRunning: true, simStopped: false, spool: true };
  for (const s of ['cloud-api', 'web-console']) state.images[REG[s]] = { Id: 'sha256:' + (s === 'cloud-api' ? '1' : '2').repeat(64), Config: { Labels: { 'org.opencontainers.image.revision': R6 } }, Os: 'linux', Architecture: arch };
  const byId = (id) => Object.values(state.images).find((v) => v.Id === id);
  const execute = (cmd, args, opts) => {
    if (cmd === 'git') return spawnSync(cmd, args, { ...opts, encoding: 'utf8' });
    state.calls.push(args.join(' '));
    if (args[0] === 'push' || args.includes('pull') && !args.includes('never') || args[0] === 'pull') throw Error('registry access attempted: ' + args.join(' '));
    if (args[0] === 'build') {
      const tag = args[args.indexOf('-t') + 1];
      const shaArg = args[args.indexOf('--build-arg') + 1].split('=')[1];
      const svc = tag.split('/')[1].split(':')[0];
      state.images[tag] = { Id: buildId(svc + shaArg), Config: { Labels: { 'org.opencontainers.image.revision': shaArg } }, Os: 'linux', Architecture: arch };
      return { status: 0, stdout: '' };
    }
    if (args[0] === 'image' && args[1] === 'inspect') { const v = state.images[args.at(-1)]; return v ? { status: 0, stdout: JSON.stringify(v) } : { status: 1, stdout: '' }; }
    if (args[0] === 'image' && args[1] === 'rm') { delete state.images[args[2]]; return { status: 0, stdout: '' }; }
    if (args[0] === 'tag') { state.images[args[2]] = state.images[args[1]] ?? byId(args[1]); assert.ok(state.images[args[2]], 'tag of unknown image'); return { status: 0, stdout: '' }; }
    if (args[0] === 'compose') {
      const files = args.flatMap((a, i) => (args[i - 1] === '-f' ? [a] : []));
      const ov = files.find((f) => /override/.test(f));
      const imgs = ov ? Object.fromEntries([...readFileSync(ov, 'utf8').matchAll(/  (cloud-api|web-console):\n    image: (\S+)/g)].map((m) => [m[1], m[2]])) : { ...REG };
      if (ov) state.lastOverride = ov;
      if (args.includes('psql')) return { status: 0, stdout: state.schema + '\n' };
      if (args.includes('config')) return { status: 0, stdout: JSON.stringify(devConfig(imgs)) };
      if (args.includes('kill')) { if (state.failHup) return { status: 1, stdout: '' }; state.hup++; return { status: 0, stdout: '' }; }
      if (args.includes('stop') && args.at(-1) === 'cloud-api') { state.apiStopped = true; return { status: 0, stdout: '' }; }
      if (args.includes('exec')) { const path = args.at(-1); return { status: 0, stdout: readFileSync(path === '/mosquitto/acl' ? r.acl : path === '/inventory/inventory.json' ? r.inventory : r.fleet, 'utf8') }; }
      if (args.includes('run') && args.includes('reset-business')) {
        state.resetCalls.push(args.slice(args.indexOf('reset-business')).join(' '));
        const counts = (n) => ({ projects: n, devices: n, alarms: n * 3 });
        if (args.includes('plan')) return { status: 0, stdout: JSON.stringify({ plan: { digest: 'd'.repeat(64), realRuntimes: 0, delete: counts(state.businessRows), keep: { portfolio_scopes: 1, equipment_types: 43 } } }) };
        if (args.at(-1) && args.includes('apply')) { if (!state.simStopped && state.simRunning) throw Error('database reset while the simulator runs'); if (!state.apiStopped) throw Error('database reset while cloud-api writes'); state.businessRows = 0; return { status: 0, stdout: JSON.stringify({ deleted: counts(2), kept: { portfolio_scopes: 1, equipment_types: 43 } }) }; }
      }
      if (args.includes('up')) {
        assert.ok(args.includes('--no-build') && args.includes('never') && args.includes('--no-deps'));
        state.upCount++;
        const targets = ['cloud-api', 'web-console'].filter((s) => args.includes(s));
        for (const s of targets) state.running[s] = imgs[s];
        if (state.failUp && state.failUp(state.upCount)) return { status: 1, stdout: '' };
        return { status: 0, stdout: '' };
      }
      if (args.includes('ps')) return { status: 0, stdout: 'c-' + args.at(-1) + '\n' };
      if (args.includes('logs') || args.includes('restart')) return { status: 0, stdout: '' };
    }
    if (args[0] === 'ps' && args.includes(`name=^${SIM_CONTAINER}$`)) return { status: 0, stdout: state.simRunning && !state.simStopped ? 'abc\n' : '' };
    if (args[0] === 'stop' && args.at(-1) === SIM_CONTAINER) { state.simStopped = true; return { status: 0, stdout: '' }; }
    if (args[0] === 'volume' && args[1] === 'ls') return { status: 0, stdout: state.spool ? SIM_SPOOL_VOLUME + '\n' : '' };
    if (args[0] === 'volume' && args[1] === 'rm') { assert.ok(state.simStopped || !state.simRunning, 'spool removed while the simulator runs'); state.spool = false; return { status: 0, stdout: '' }; }
    if (args[0] === 'inspect') {
      const svc = args.at(-1).slice(2); const ref = state.running[svc]; const v = state.images[ref];
      if (args[2] === '{{json .}}') return { status: 0, stdout: JSON.stringify({ Image: v.Id, Config: { Image: ref, Labels: { ...v.Config.Labels, 'com.docker.compose.project.working_dir': '/srv/u-dao/src/old/deploy/compose', 'com.docker.compose.project.config_files': '/srv/u-dao/src/old/deploy/compose/docker-compose.prod.yml,/srv/u-dao/src/old/deploy/compose/docker-compose.crane-dev.yml' } }, Os: v.Os, Architecture: v.Architecture }) };
      if (args[2] === '{{.Image}}') return { status: 0, stdout: v.Id + '\n' };
      if (args[2] === '{{.Config.Image}}') return { status: 0, stdout: ref + '\n' };
      return { status: 0, stdout: v.Config.Labels['org.opencontainers.image.revision'] + '\n' };
    }
    throw Error('unexpected docker ' + args.join(' '));
  };
  const probe = (u) => !smokeOk(state) ? { status: 502, body: '' } : u === 'https://crane.youdaozd.com/' ? { status: 200, body: '' } : u.includes('openid') ? { status: 200, body: JSON.stringify({ issuer: 'https://crane.youdaozd.com/realms/crane' }) } : u.includes('/api/') ? { status: 401 } : { status: 404 };
  return { state, opts: { root: r.root, execute, probe, log: () => {}, now: () => new Date() } };
}
const read = (r, name) => JSON.parse(readFileSync(join(r.state, name), 'utf8'));

test('build: full-SHA local tags, explicit GOPROXY build-arg (Go images only), nothing pushed, existing SHA never overwritten', (t) => {
  const r = repo(t); const d = fakeDocker(r);
  const out = build(r.env, ['cloud-api', 'web-console', 'device-simulator'], d.opts, 'https://goproxy.cn,direct');
  for (const s of ['cloud-api', 'web-console', 'device-simulator']) { assert.equal(out.images[s].tag, localTag(s, r.sha())); assert.equal(out.images[s].platform, 'linux/amd64'); }
  const builds = d.state.calls.filter((c) => c.startsWith('build'));
  assert.ok(builds.every((c) => c.includes('--pull=false')));
  assert.equal(builds.filter((c) => c.includes('GOPROXY=https://goproxy.cn,direct')).length, 2, 'cloud-api + device-simulator only');
  assert.ok(!builds.find((c) => c.includes('Dockerfile.web-production')).includes('GOPROXY'));
  assert.ok(!builds.some((c) => /GOSUMDB|GOFLAGS|GONOSUMDB|GOINSECURE/.test(c)), 'checksum verification untouched');
  assert.equal(read(r, `build-${r.sha()}.json`).goproxy, 'https://goproxy.cn,direct');
  for (const bad of ['http://goproxy.cn', 'https://u:p@goproxy.cn', 'https://goproxy.cn;rm -rf /', 'https://goproxy.cn direct'])
    assert.throws(() => buildGoproxy({}, bad), /GOPROXY/, bad);
  assert.equal(buildGoproxy({ CRANE_BUILD_GOPROXY: 'https://goproxy.cn,direct' }), 'https://goproxy.cn,direct');
  assert.equal(buildGoproxy({}), null);
  assert.equal(build(r.env, ['cloud-api'], d.opts).images['cloud-api'].reused, true);
  let n = 0; const drift = fakeDocker(r, { buildId: () => 'sha256:' + String(++n).padStart(64, '0') }); drift.state.images = d.state.images;
  assert.throws(() => build(r.env, ['cloud-api'], drift.opts), /different content/);
  assert.ok(!existsSync(join(r.state, 'deploy.lock')), 'lock released after a refused build');
  writeFileSync(join(r.root, 'dirty.txt'), 'x');
  assert.throws(() => build(r.env, ['cloud-api'], d.opts), /clean Git worktree/);
});

test('first local apply: R6 baseline captured as local tags; failed smoke restores R6 locally, current stays R6, attempt FAILED', (t) => {
  const r = repo(t);
  let broken = false;
  const d = fakeDocker(r, { smokeOk: () => !broken });
  build(r.env, ['cloud-api', 'web-console'], d.opts);
  broken = true;
  d.state.failUp = null;
  // Smoke fails only while the new images run: restore makes it pass again.
  d.opts.probe = ((orig) => (u) => (d.state.running['cloud-api'].startsWith('crane-local/cloud-api:baseline') ? { ...fakeDocker(r).opts.probe(u) } : orig(u)))(d.opts.probe);
  assert.throws(() => apply(r.env, undefined, d.opts), (e) => e.code === 3 && /restore of crane-baseline: PASS/.test(e.message));
  const base = read(r, 'baseline.json');
  assert.equal(base.images['cloud-api'].ref, REG['cloud-api']);
  assert.equal(base.images['cloud-api'].tag, `crane-local/cloud-api:baseline-${R6}`);
  assert.equal(base.images['cloud-api'].id, 'sha256:' + '1'.repeat(64));
  assert.equal(base.db_schema_version, 12);
  assert.match(base.compose_working_dir, /\/srv\/u-dao\/src\/old/);
  assert.match(base.env_sha256, /^[a-f0-9]{64}$/);
  assert.equal(read(r, 'current.json').kind, 'crane-baseline', 'current is NOT the failed deploy');
  assert.equal(existsSync(join(r.state, 'previous.json')), false);
  const attempt = read(r, 'attempt.json');
  assert.equal(attempt.status, 'FAILED'); assert.equal(attempt.restored, 'PASS'); assert.match(attempt.reason, /smoke failed/);
  assert.equal(d.state.running['cloud-api'], `crane-local/cloud-api:baseline-${R6}`, 'running the local R6 tag, no registry pull');
  assert.match(readFileSync(join(r.state, ACTIVE_OVERRIDE), 'utf8'), /cloud-api:baseline-/);
  assert.ok(!d.state.calls.some((c) => /^pull|ghcr\.io.*pull/.test(c)));
});

test('apply confirms only after image IDs + schema + smoke; then rollback to R6 baseline and to a previous local deploy', (t) => {
  const r = repo(t); const d = fakeDocker(r);
  build(r.env, ['cloud-api', 'web-console'], d.opts);
  // A running image that is not the built one is detected, nothing confirmed, R6 restored.
  const orig = d.opts.execute; let lie = true;
  d.opts.execute = (c, a, o) => (lie && c === 'docker' && a[0] === 'inspect' && a[2] === '{{.Image}}' && d.state.running['cloud-api'].startsWith('crane-local/cloud-api:' + r.sha())) ? { status: 0, stdout: 'sha256:' + 'f'.repeat(64) + '\n' } : orig(c, a, o);
  assert.throws(() => apply(r.env, undefined, d.opts), /is not the built/);
  assert.equal(read(r, 'current.json').kind, 'crane-baseline');
  lie = false;
  const first = apply(r.env, undefined, d.opts);
  assert.equal(first.images['cloud-api'].tag, localTag('cloud-api', r.sha()));
  assert.equal(read(r, 'current.json').source_sha, r.sha());
  assert.equal(read(r, 'previous.json').kind, 'crane-baseline');
  assert.equal(read(r, 'attempt.json').status, 'CONFIRMED');
  const active = readFileSync(join(r.state, ACTIVE_OVERRIDE), 'utf8');
  assert.match(active, /pull_policy: never/); assert.doesNotMatch(active, /environment|ports|volumes|secrets|networks/);
  assert.equal(statSync(join(r.state, 'current.json')).mode & 0o777, 0o600);
  const one = r.sha();
  writeFileSync(join(r.root, 'apps/web-console/a.ts'), 'y\n'); r.git('commit', '-qam', 'web only');
  build(r.env, ['web-console'], d.opts);
  const p = plan(r.env, d.opts);
  assert.equal(p.images['cloud-api'].carried, true, 'unchanged cloud-api keeps the running image');
  apply(r.env, undefined, d.opts);
  assert.equal(read(r, 'previous.json').source_sha, one);
  assert.deepEqual(changedServices({ root: r.root, execute: d.opts.execute }, one, r.sha(), Object.keys(BUILDABLE)), ['web-console']);
  rollback(r.env, d.opts);
  assert.equal(d.state.running['web-console'], localTag('web-console', one));
  assert.equal(read(r, 'current.json').source_sha, one);
  // schema moved: image rollback refused
  d.state.schema = 13;
  assert.throws(() => rollback(r.env, d.opts), /schema 13/);
});

test('single-instance lock; maintenance and Fleet/ACL updates use the ACTIVE override (no registry fallback)', (t) => {
  const r = repo(t); const d = fakeDocker(r);
  assert.throws(() => composeMaintenance(r.env, ['restart', 'cloud-api'], d.opts), /active-override.yml missing/);
  build(r.env, ['cloud-api', 'web-console'], d.opts);
  mkdirSync(join(r.state, 'deploy.lock')); writeFileSync(join(r.state, 'deploy.lock/owner.json'), '{"operation":"apply","pid":1}');
  assert.throws(() => apply(r.env, undefined, d.opts), (e) => e.code === 2 && /holds .*deploy.lock/.test(e.message));
  rmSync(join(r.state, 'deploy.lock'), { recursive: true });
  apply(r.env, undefined, d.opts);
  composeMaintenance(r.env, ['restart', 'cloud-api'], d.opts);
  assert.match(d.state.calls.at(-1), new RegExp(`-f ${join(r.state, ACTIVE_OVERRIDE).replace(/[.]/g, '\\.')} restart cloud-api`));
  for (const bad of [['down'], ['pull'], ['up', '-d', 'cloud-api'], ['rm', '-f'], ['down', '-v']])
    assert.throws(() => composeMaintenance(r.env, bad, d.opts), /refuses/, bad.join(' '));
  composeMaintenance(r.env, ['up', '-d', '--no-deps', 'cloud-api'], d.opts);
  assert.match(d.state.calls.at(-1), /up --no-build --pull never -d --no-deps cloud-api/);
  // Fleet: add one SIM identity, written in place (same inode), ACL reloaded, cloud-api recreated.
  const newFleet = join(r.dir, 'fleet.new.json'), newAcl = join(r.dir, 'acl.new');
  writeFileSync(newFleet, fleetDoc(['SIM-OLD', 'SIM-UAT-0001'])); writeFileSync(newAcl, aclDoc(['SIM-OLD', 'SIM-UAT-0001']));
  const inode = statSync(r.fleet).ino;
  const upsBefore = d.state.upCount;
  assert.deepEqual(updateFleet(r.env, newFleet, newAcl, d.opts), { added: 1 });
  assert.equal(statSync(r.fleet).ino, inode, 'bind-mounted file keeps its inode');
  assert.equal(readFileSync(r.fleet, 'utf8'), fleetDoc(['SIM-OLD', 'SIM-UAT-0001']));
  assert.equal(d.state.hup, 1);
  assert.equal(d.state.upCount, upsBefore + 1);
  assert.ok(d.state.calls.some((c) => /up -d --no-build --pull never --no-deps --force-recreate --wait cloud-api/.test(c) && c.includes(ACTIVE_OVERRIDE)));
  assert.equal(d.state.running['cloud-api'], localTag('cloud-api', r.sha()), 'same confirmed image after the Fleet refresh');
  assert.ok(readFileSync(r.dir + '/' + spawnSync('ls', [r.dir], { encoding: 'utf8' }).stdout.split('\n').find((f) => f.startsWith('fleet.json.bak-')), 'utf8').includes('SIM-OLD'));
  // removing or changing an existing identity, or an ACL that does not match, is refused
  writeFileSync(newFleet, fleetDoc(['SIM-UAT-0001'])); writeFileSync(newAcl, aclDoc(['SIM-UAT-0001']));
  assert.throws(() => updateFleet(r.env, newFleet, newAcl, d.opts), /would be removed/);
  writeFileSync(newFleet, fleetDoc(['SIM-OLD', 'SIM-UAT-0001', 'SIM-UAT-0002'])); writeFileSync(newAcl, aclDoc(['SIM-OLD', 'SIM-UAT-0001']));
  assert.throws(() => updateFleet(r.env, newFleet, newAcl, d.opts), /ACL does not match/);
  writeFileSync(newAcl, aclDoc(['SIM-OLD', 'SIM-UAT-0001', 'SIM-UAT-0002']).replace('heartbeat', '#'));
  assert.throws(() => updateFleet(r.env, newFleet, newAcl, d.opts), /ACL does not match/);
});

test('crane-dev business reset: guarded plan/apply, simulator stopped before DB, empty Inventory/Fleet/ACL in place, spool removed, cloud-api recreated', (t) => {
  const r = repo(t); const d = fakeDocker(r);
  assert.throws(() => resetBusiness(r.env, 'plan', {}, d.opts), /active-override.yml missing/, 'no reset before a confirmed local deploy');
  build(r.env, ['cloud-api', 'web-console'], d.opts);
  apply(r.env, undefined, d.opts);
  const p = resetBusiness(r.env, 'plan', {}, d.opts);
  assert.equal(p.digest, 'd'.repeat(64)); assert.equal(p.delete.projects, 2);
  assert.equal(d.state.businessRows, 2, 'plan is read-only');
  for (const bad of [{}, { approve: p.digest, actor: 'admin' }, { approve: p.digest, actor: 'admin', confirm: 'production' }, { approve: p.digest, confirm: 'crane-dev' }])
    assert.throws(() => resetBusiness(r.env, 'apply', bad, d.opts), /--confirm crane-dev/, JSON.stringify(bad));
  assert.equal(d.state.businessRows, 2);
  const inodes = [r.inventory, r.fleet, r.acl].map((f) => statSync(f).ino);
  const upsBefore = d.state.upCount;
  resetBusiness(r.env, 'apply', { approve: p.digest, actor: 'admin', confirm: 'crane-dev' }, d.opts);
  assert.ok(d.state.simStopped, 'simulator stopped'); assert.equal(d.state.spool, false, 'spool volume removed');
  assert.equal(d.state.businessRows, 0);
  assert.deepEqual(JSON.parse(readFileSync(r.inventory, 'utf8')), { tenantId: 'demo', projects: [], assetDevices: [], runtimeDevices: [] });
  assert.deepEqual(readFleet(r.fleet), { tenantId: 'demo', devices: [] });
  assert.equal(readFileSync(r.acl, 'utf8'), '# Generated development ACL: exact tenant/project/device scopes; not production PKI.\nuser cloud-api\n');
  assert.doesNotMatch(readFileSync(r.acl, 'utf8'), /topic|[#+](?!.*PKI)/);
  assert.deepEqual([r.inventory, r.fleet, r.acl].map((f) => statSync(f).ino), inodes, 'bind-mounted files keep their inode');
  assert.ok(d.state.hup >= 1); assert.equal(d.state.upCount, upsBefore + 1);
  assert.ok(d.state.calls.some((c) => /--force-recreate --wait cloud-api/.test(c) && c.includes(ACTIVE_OVERRIDE)));
  assert.equal(d.state.running['cloud-api'], localTag('cloud-api', r.sha()));
  assert.ok(d.state.resetCalls.some((c) => c === `reset-business apply --approve ${p.digest} --actor admin`));
  assert.equal(JSON.parse(readFileSync(join(r.state, 'reset-state.json'), 'utf8')).status, 'COMPLETE');
  // Normal Fleet updates still refuse to remove identities; the empty Fleet is a legal base for growth.
  const nf = join(r.dir, 'f2.json'), na = join(r.dir, 'a2');
  writeFileSync(nf, fleetDoc(['SIM-UAT-0001'])); writeFileSync(na, aclDoc(['SIM-UAT-0001']));
  assert.deepEqual(updateFleet(r.env, nf, na, d.opts), { added: 1 });
  writeFileSync(nf, JSON.stringify({ tenantId: 'demo', devices: [] })); writeFileSync(na, aclDoc([]));
  assert.throws(() => updateFleet(r.env, nf, na, d.opts), /would be removed/);
});

test('reset-business: DB committed but a file step fails → state DB_RESET_FILES_PENDING (not complete); finish repeats only the file steps', (t) => {
  const r = repo(t); const d = fakeDocker(r);
  build(r.env, ['cloud-api', 'web-console'], d.opts);
  apply(r.env, undefined, d.opts);
  const p = resetBusiness(r.env, 'plan', {}, d.opts);
  d.state.failHup = true; // broker reload fails after the DB reset
  assert.throws(() => resetBusiness(r.env, 'apply', { approve: p.digest, actor: 'admin', confirm: 'crane-dev' }, d.opts), /Docker step failed/);
  assert.equal(d.state.businessRows, 0, 'database step committed');
  const st = JSON.parse(readFileSync(join(r.state, 'reset-state.json'), 'utf8'));
  assert.equal(st.status, 'DB_RESET_FILES_PENDING', 'a DB-only success is never recorded as a complete reset');
  // apply again is impossible (digest single use on the server) — finish is the recovery path.
  assert.throws(() => resetBusiness(r.env, 'finish', { actor: 'admin' }, d.opts), /--confirm crane-dev/);
  d.state.failHup = false;
  const calls = d.state.resetCalls.length;
  resetBusiness(r.env, 'finish', { actor: 'admin', confirm: 'crane-dev' }, d.opts);
  assert.ok(!d.state.resetCalls.slice(calls).some((c) => c.includes('apply')), 'finish never re-runs the database step');
  assert.equal(JSON.parse(readFileSync(join(r.state, 'reset-state.json'), 'utf8')).status, 'COMPLETE');
  assert.equal(d.state.hup, 1);
  assert.deepEqual(readFleet(r.fleet), { tenantId: 'demo', devices: [] });
  // finish refuses when the database still has business rows (that is not a file-step recovery).
  writeFileSync(join(r.state, 'reset-state.json'), JSON.stringify({ status: 'DB_RESET_FILES_PENDING', deleted: {} }));
  d.state.businessRows = 3;
  assert.throws(() => resetBusiness(r.env, 'finish', { actor: 'admin', confirm: 'crane-dev' }, d.opts), /still holds business rows/);
});

test('local images only for crane-dev; production validation keeps requiring digests', () => {
  const sha = 'a'.repeat(40);
  const imgs = { 'cloud-api': localTag('cloud-api', sha), 'web-console': localTag('web-console', sha) };
  assert.ok(validateLocalDevelopment(devConfig(imgs), sha));
  const prod = devConfig(imgs); prod.services['cloud-api'].environment.CRANE_DEPLOYMENT_ENVIRONMENT = undefined;
  assert.throws(() => validateLocalDevelopment(prod, sha), /crane-dev/);
  const noPolicy = devConfig(imgs); delete noPolicy.services['web-console'].pull_policy;
  assert.throws(() => validateLocalDevelopment(noPolicy, sha), /never be pulled/);
  assert.throws(() => validateProduction(devConfig(imgs)), /immutable image/);
  assert.equal(overrideYaml(imgs).split('\n').filter((l) => /^\s{4}\S/.test(l)).length, 4);
});
