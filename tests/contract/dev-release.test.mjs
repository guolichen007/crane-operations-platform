import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, realpathSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, dirname, join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import YAML from 'yaml';
import { record, plan, apply, rollback, verifyBackup, Stop, UPDATABLE } from '../../deploy/scripts/dev-upgrade.mjs';
import { verifyRelease } from '../../deploy/scripts/release-check.mjs';
import { productionProject, validateProduction, validateDevelopment, validateDevOverlay } from '../../tools/production-contract.mjs';
import { checkInputs, DEV_REGISTRY, schemaVersion } from '../../tools/dev-candidate.mjs';

const UP = JSON.parse(readFileSync('deploy/production/images.json', 'utf8'));
const OLD = 'c'.repeat(40);

function fixture(t, { schemaMax = 12 } = {}) {
  const directory = mkdtempSync(join(tmpdir(), 'crane-d1-'));
  t.after(() => {
    assert.equal(realpathSync(dirname(directory)), realpathSync(tmpdir()));
    assert.ok(basename(directory).startsWith('crane-d1-'));
    rmSync(directory, { recursive: true, force: true });
  });
  const root = join(directory, 'checkout'); mkdirSync(root);
  const git = (...args) => {
    const r = spawnSync('git', ['-C', root, '-c', 'user.name=Anonymous', '-c', 'user.email=ci@example.test', '-c', 'commit.gpgsign=false', ...args], { encoding: 'utf8' });
    assert.equal(r.status, 0, 'anonymous fixture Git operation failed');
    return r.stdout.trim();
  };
  git('init', '--quiet'); mkdirSync(join(root, 'deploy/compose'), { recursive: true });
  writeFileSync(join(root, 'deploy/compose/docker-compose.prod.yml'), 'name: u-dao-crane-ops\n');
  git('add', '.'); git('commit', '--quiet', '-m', 'anonymous fixture');
  const sha = git('rev-parse', 'HEAD');
  const images = ['cloud-api', 'web-console'].map(name => {
    const sbom_file = name + '.cdx.json', data = '{"bomFormat":"CycloneDX"}\n'; writeFileSync(join(directory, sbom_file), data);
    return { schema_version: 1, source_sha: sha, image_name: DEV_REGISTRY + '/' + name, image_digest: 'sha256:' + 'b'.repeat(64), sbom_file, sbom_sha256: createHash('sha256').update(data).digest('hex') };
  });
  const manifest = { schema_version: 1, kind: 'crane-dev-candidate', source_sha: sha, schema_version_max: schemaMax, images, upstream: UP };
  const manifestPath = join(directory, 'dev-manifest.json');
  const save = () => writeFileSync(manifestPath, JSON.stringify(manifest)); save();
  const envFile = join(directory, 'crane.env'); writeFileSync(envFile, `CRANE_BACKUP_ROOT=${directory.replaceAll('\\', '/')}\n`, { mode: 0o600 });
  const previous = { schema_version: 1, kind: 'crane-running-record', project: productionProject, source_sha: OLD,
    images: { 'cloud-api': 'ghcr.io/x/dev/cloud-api@sha256:' + '1'.repeat(64), 'web-console': 'ghcr.io/x/dev/web-console@sha256:' + '2'.repeat(64) }, db_schema_version: 12, recorded_at: new Date().toISOString() };
  const previousPath = join(directory, 'previous.json');
  const savePrev = () => writeFileSync(previousPath, JSON.stringify(previous)); savePrev();
  return { directory, root, sha, manifest, manifestPath, save, envFile, previous, previousPath, savePrev, git };
}

// Resolved development config with the given cloud/web images (stand-in for `docker compose config`).
function devConfig(cloud, web) {
  const services = {};
  for (const name of ['postgres', 'keycloak', 'mosquitto', 'cloud-api', 'web-console'])
    services[name] = { image: UP[name] ?? 'x@sha256:' + 'a'.repeat(64), logging: { driver: 'json-file', options: { 'max-size': '10m', 'max-file': '5' } }, networks: { backend: null } };
  services['cloud-api'].image = cloud; services['web-console'].image = web;
  services['cloud-api'].environment = { APP_ENV: 'production', OIDC_CLIENT_SECRET_FILE: '/run/secrets/oidc_client_secret', OIDC_ISSUER: 'https://crane.youdaozd.com/realms/crane',
    OIDC_CLIENT_ID: 'crane-bff', PUBLIC_URL: 'https://crane.youdaozd.com', CRANE_DEPLOYMENT_ENVIRONMENT: 'crane-dev' };
  services.keycloak.environment = { KC_HOSTNAME_STRICT: 'true', KC_DB: 'postgres', KC_HOSTNAME: 'https://crane.youdaozd.com' };
  services.keycloak.volumes = [{ target: '/opt/keycloak/themes/u-dao', source: '/anon/themes', read_only: true }];
  for (const name of ['keycloak', 'cloud-api', 'web-console']) services[name].networks.frontend = null;
  return { name: productionProject, services, networks: { frontend: { external: true, name: 'u-dao-crane-frontend' }, backend: { internal: true } },
    secrets: Object.fromEntries(Array.from({ length: 6 }, (_, i) => ['s' + i, { file: '/anon/' + i }])) };
}

/** Fake Docker: tracks which images run, the DB schema and every invocation. */
function fakeDocker(f, { schema = 12, running = f.previous.images, revision = OLD, candidateRuns = true } = {}) {
  const state = { calls: [], running: { ...running }, revision, schema };
  const target = Object.fromEntries(f.manifest.images.map(i => [i.image_name.endsWith('/cloud-api') ? 'cloud-api' : 'web-console', i.image_name + '@' + i.image_digest]));
  state.envImages = { ...target };
  const execute = (command, args) => {
    state.calls.push([command, ...args]);
    if (command === 'python3') return { status: state.drillStatus ?? 0, stdout: '' };
    const has = (x) => args.includes(x);
    if (has('config')) return { status: 0, stdout: JSON.stringify(devConfig(state.envImages['cloud-api'], state.envImages['web-console'])) };
    if (has('ps')) return { status: 0, stdout: 'id-' + args.at(-1) + '\n' };
    if (args[0] === 'inspect') {
      const service = args.at(-1).slice(3);
      return { status: 0, stdout: args[2].includes('revision') ? state.revision + '\n' : state.running[service] + '\n' };
    }
    if (has('psql')) return { status: 0, stdout: state.schema + '\n' };
    if (has('pull')) return { status: 0, stdout: '' };
    if (has('up')) {
      if (candidateRuns) state.running = { ...state.envImages };
      if (state.envImages['cloud-api'] === target['cloud-api']) { state.revision = f.sha; if (state.migrateTo) state.schema = state.migrateTo; }
      else state.revision = OLD;
      return { status: 0, stdout: '' };
    }
    throw Error('unexpected docker call ' + args.join(' '));
  };
  return { state, execute };
}

const goodProbe = (url) => url === 'https://crane.youdaozd.com/' ? { status: 200, body: '<html>' }
  : url.includes('openid-configuration') ? { status: 200, body: JSON.stringify({ issuer: 'https://crane.youdaozd.com/realms/crane' }) }
  : url.includes('/api/') ? { status: 401, body: '' } : { status: 403, body: '' };
const opts = (f, docker, extra = {}) => ({ root: f.root, execute: docker.execute, diskGate: () => ({}), probe: goodProbe, log: () => {}, ...extra });

test('开发候选与生产发布清单互不通用', t => {
  const f = fixture(t);
  assert.equal(verifyRelease(f.manifestPath, f.root, 'development').source_sha, f.sha);
  assert.throws(() => verifyRelease(f.manifestPath, f.root, 'production'), /cannot be used as a production release/);
  delete f.manifest.kind; f.save();
  assert.throws(() => verifyRelease(f.manifestPath, f.root, 'development'), /development candidate/);
  f.manifest.kind = 'crane-dev-candidate'; f.manifest.images[0].image_name = 'ghcr.io/guolichen007/crane-operations-platform/cloud-api'; f.save();
  assert.throws(() => verifyRelease(f.manifestPath, f.root, 'development'), /image\/source mismatch/);
});

test('plan 只读：核对候选 digest、旧版本固定、无迁移时给出计划且不执行写操作', t => {
  const f = fixture(t), d = fakeDocker(f);
  const p = plan(f.manifestPath, f.envFile, f.previousPath, opts(f, d));
  assert.equal(p.migration, false);
  assert.deepEqual(p.services, UPDATABLE);
  assert.equal(d.state.calls.some(c => c.includes('up') || c.includes('pull')), false, 'plan must never pull or up');
  for (const c of d.state.calls.filter(c => c[0] === 'docker' && c.includes('compose'))) {
    assert.equal(c[c.indexOf('--project-name') + 1], 'u-dao-crane-ops');
    assert.ok(c.some(a => a.endsWith('docker-compose.crane-dev.yml')), 'development overlay must always be applied');
  }
});

test('plan STOP：运行镜像偏离固定记录、schema 变化、候选 schema 更旧、共享服务镜像漂移', t => {
  const f = fixture(t);
  assert.throws(() => plan(f.manifestPath, f.envFile, f.previousPath, opts(f, fakeDocker(f, { running: { ...f.previous.images, 'web-console': 'ghcr.io/x/dev/web-console@sha256:' + '9'.repeat(64) } }))), Stop);
  assert.throws(() => plan(f.manifestPath, f.envFile, f.previousPath, opts(f, fakeDocker(f, { schema: 13 }))), /schema changed/);
  f.manifest.schema_version_max = 11; f.save();
  f.previous.db_schema_version = 12; f.savePrev();
  assert.throws(() => plan(f.manifestPath, f.envFile, f.previousPath, opts(f, fakeDocker(f))), /older than database schema/);
  f.manifest.schema_version_max = 12; f.manifest.upstream = { ...UP, keycloak: 'quay.io/keycloak/keycloak:99@sha256:' + 'e'.repeat(64) }; f.save();
  assert.throws(() => plan(f.manifestPath, f.envFile, f.previousPath, opts(f, fakeDocker(f))), /shared services are out of scope/);
});

test('plan 拒绝：环境文件 digest 与候选不符、shell 变量覆盖、APP_ENV 被改为 development', t => {
  const f = fixture(t), d = fakeDocker(f);
  d.state.envImages['cloud-api'] = 'ghcr.io/x/dev/cloud-api@sha256:' + 'f'.repeat(64);
  assert.throws(() => plan(f.manifestPath, f.envFile, f.previousPath, opts(f, d)), /does not match the reviewed candidate/);
  process.env.CRANE_CLOUD_IMAGE = 'x'; t.after(() => delete process.env.CRANE_CLOUD_IMAGE);
  assert.throws(() => plan(f.manifestPath, f.envFile, f.previousPath, opts(f, fakeDocker(f))), /would override/);
  delete process.env.CRANE_CLOUD_IMAGE;
  const c = devConfig('a@sha256:' + 'a'.repeat(64), 'b@sha256:' + 'b'.repeat(64));
  c.services['cloud-api'].environment.APP_ENV = 'development';
  assert.throws(() => validateDevelopment(c), /'development'/);
});

test('apply 无迁移：只 pull/up cloud-api 与 web-console（--no-deps），校验 revision 与冒烟', t => {
  const f = fixture(t), d = fakeDocker(f), lines = [];
  apply(f.manifestPath, f.envFile, f.previousPath, undefined, opts(f, d, { log: l => lines.push(l) }));
  const writes = d.state.calls.filter(c => c.includes('pull') || c.includes('up'));
  assert.equal(writes.length, 2);
  for (const c of writes) {
    assert.deepEqual(c.slice(-2), UPDATABLE, 'only cloud-api and web-console');
    for (const s of ['postgres', 'keycloak', 'mosquitto']) assert.equal(c.includes(s), false);
  }
  assert.ok(writes[1].includes('--no-deps') && writes[1].includes('--no-build'));
  assert.ok(lines.some(l => l.startsWith('DEV_APPLY=PASS')));
});

test('apply 涉及迁移：缺备份 STOP，备份过旧/来源错误拒绝，演练失败 STOP，全部满足才升级', t => {
  const f = fixture(t, { schemaMax: 13 });
  assert.throws(() => apply(f.manifestPath, f.envFile, f.previousPath, undefined, opts(f, fakeDocker(f))), /requires a fresh backup/);
  const backup = join(f.directory, 'crane-backup-20261110T010000Z-0123abcd'); mkdirSync(backup);
  const now = new Date('2026-11-10T02:00:00Z');
  const meta = { schema_version: 1, timestamp: '2026-11-10T01:00:00+00:00', source_sha: OLD, databases: {} };
  for (const label of ['application', 'keycloak']) {
    writeFileSync(join(backup, label + '.dump'), label);
    meta.databases[label] = { file: label + '.dump', sha256: createHash('sha256').update(label).digest('hex'), snapshot: label === 'application' ? { schema_version: 12 } : {} };
  }
  const writeMeta = () => { writeFileSync(join(backup, 'metadata.json'), JSON.stringify(meta)); writeFileSync(join(backup, 'metadata.sha256'), createHash('sha256').update(JSON.stringify(meta)).digest('hex') + '\n'); };
  writeMeta();
  assert.ok(verifyBackup(backup, f.previous, 12, now));
  assert.throws(() => verifyBackup(backup, f.previous, 12, new Date('2026-11-10T09:00:00Z')), /at most/);
  meta.source_sha = 'd'.repeat(40); writeMeta();
  assert.throws(() => verifyBackup(backup, f.previous, 12, now), /previous running version/);
  meta.source_sha = OLD; writeMeta();
  writeFileSync(join(backup, 'application.dump'), 'tampered');
  assert.throws(() => verifyBackup(backup, f.previous, 12, now), /checksum/);
  writeFileSync(join(backup, 'application.dump'), 'application');
  const failing = fakeDocker(f); failing.state.drillStatus = 1;
  assert.throws(() => apply(f.manifestPath, f.envFile, f.previousPath, backup, opts(f, failing, { now: () => now })), /restore drill failed/);
  assert.equal(failing.state.calls.some(c => c.includes('up')), false, 'no upgrade after failed drill');
  const ok = fakeDocker(f); ok.state.migrateTo = 13;
  apply(f.manifestPath, f.envFile, f.previousPath, backup, opts(f, ok, { now: () => now }));
  assert.ok(ok.state.calls.some(c => c[0] === 'python3' && c.includes('drill')));
});

test('apply 冒烟失败返回 3 并给出回滚指令；迁移后的回滚在 schema 不一致时 STOP', t => {
  const f = fixture(t);
  const bad = (url) => url.includes('/api/') ? { status: 200, body: '[]' } : goodProbe(url);
  assert.throws(() => apply(f.manifestPath, f.envFile, f.previousPath, undefined, opts(f, fakeDocker(f), { probe: bad })), e => e.code === 3 && /Rollback/.test(e.message));
  // rollback requires the previous SHA checkout; the fixture HEAD is the candidate, so it is refused before Docker.
  const d = fakeDocker(f);
  assert.throws(() => rollback(f.envFile, f.previousPath, opts(f, d)), /differs from release source SHA/);
  assert.equal(d.state.calls.length, 0);
  f.previous.source_sha = f.sha; f.savePrev();
  const moved = fakeDocker(f, { schema: 13 }); moved.state.envImages = { ...f.previous.images };
  assert.throws(() => rollback(f.envFile, f.previousPath, opts(f, moved)), e => e instanceof Stop && /compatibility is not proven/.test(e.message));
  assert.equal(moved.state.calls.some(c => c.includes('up')), false);
  const same = fakeDocker(f, { schema: 12 }); same.state.envImages = { ...f.previous.images };
  rollback(f.envFile, f.previousPath, opts(f, same));
  assert.deepEqual(same.state.calls.find(c => c.includes('up')).slice(-2), UPDATABLE);
});

test('record 固定当前运行 digest 与 schema，拒绝非 digest 运行镜像且不覆盖已有记录', t => {
  const f = fixture(t), out = join(f.directory, 'pinned.json');
  const r = record(f.envFile, out, opts(f, fakeDocker(f)));
  assert.equal(r.db_schema_version, 12); assert.deepEqual(r.images, f.previous.images);
  assert.throws(() => record(f.envFile, out, opts(f, fakeDocker(f))), /EEXIST/);
  assert.throws(() => record(f.envFile, join(f.directory, 'p2.json'), opts(f, fakeDocker(f, { running: { 'cloud-api': 'ghcr.io/x/cloud-api:latest', 'web-console': f.previous.images['web-console'] } }))), /immutable/);
});

test('单 Origin 开发 overlay 只加哨兵；生产校验不放宽', () => {
  const overlay = YAML.parse(readFileSync('deploy/compose/docker-compose.crane-dev.yml', 'utf8'));
  validateDevOverlay(overlay);
  for (const mutate of [
    o => { o.services['cloud-api'].environment.APP_ENV = 'development'; },
    o => { o.services['cloud-api'].image = 'x:latest'; },
    o => { o.services['web-console'] = { environment: { A: '1' } }; },
    o => { o.services['cloud-api'].ports = ['127.0.0.1:9999:8081']; },
    o => { o.volumes = { extra: {} }; },
  ]) { const o = structuredClone(overlay); mutate(o); assert.throws(() => validateDevOverlay(o)); }
  const good = devConfig('a@sha256:' + 'a'.repeat(64), 'b@sha256:' + 'b'.repeat(64));
  validateDevelopment(good);
  for (const mutate of [
    c => { delete c.services['cloud-api'].environment.CRANE_DEPLOYMENT_ENVIRONMENT; },
    c => { c.services['cloud-api'].environment.OIDC_ISSUER = 'https://b4.crane.youdaozd.com/realms/crane-preview'; },
    c => { c.services['cloud-api'].environment.PUBLIC_URL = 'https://b4.crane.youdaozd.com'; },
    c => { c.services.keycloak.environment.KC_HOSTNAME_STRICT = 'false'; },
    c => { c.services['cloud-api'].environment.DEV_API_TOKEN = 'x'; },
    c => { c.services.postgres.ports = [{ host_ip: '0.0.0.0', published: '5432' }]; },
    c => { c.services['web-console'].environment = { CRANE_DEPLOYMENT_ENVIRONMENT: 'crane-dev' }; },
  ]) { const c = structuredClone(good); mutate(c); assert.throws(() => validateDevelopment(c)); }
  const prod = structuredClone(good); delete prod.services['cloud-api'].environment.CRANE_DEPLOYMENT_ENVIRONMENT;
  validateProduction(prod);
});

test('开发候选工作流：仅 dev/crane-* 标签、先核九项 CI、复用生产构建链、不部署不用 latest', () => {
  const wf = YAML.parse(readFileSync('.github/workflows/dev-candidate.yml', 'utf8'));
  assert.deepEqual(Object.keys(wf.on), ['push']); assert.deepEqual(wf.on.push.tags, ['dev/crane-*']); assert.equal(wf.on.push.branches, undefined);
  const ci = YAML.parse(readFileSync('.github/workflows/ci.yml', 'utf8'));
  assert.equal(ci.on.push.tags, undefined, 'tag pushes must not trigger ci.yml / production-release');
  assert.ok(ci.jobs['production-release'].if.includes('refs/heads/main'));
  const steps = wf.jobs['dev-candidate'].steps, gate = steps.findIndex(s => s.id === 'gate'), build = steps.findIndex(s => (s.run || '').includes('tools/dev-candidate.mjs'));
  assert.ok(gate >= 0 && build > gate);
  for (const job of ['a1-security', 'b4-business-acceptance', 'backend', 'edge', 'frontend', 'integration', 'r2-db-test', 'repository-contract', 'security']) assert.ok(steps[gate].run.includes(job));
  const text = readFileSync('.github/workflows/dev-candidate.yml', 'utf8');
  for (const forbidden of ['release.mjs', 'task.mjs release', ':latest', 'ssh ', 'docker compose', 'up -d', 'dev-upgrade']) assert.ok(!text.includes(forbidden), forbidden);
  const source = readFileSync('tools/dev-candidate.mjs', 'utf8');
  assert.match(source, /from '\.\/release-lib\.mjs'/); assert.doesNotMatch(source, /docker', \['build/);
  assert.match(readFileSync('tools/release.mjs', 'utf8'), /from '\.\/release-lib\.mjs'/);
  assert.equal(DEV_REGISTRY, 'ghcr.io/guolichen007/crane-operations-platform/dev');
  const lib = readFileSync('tools/release-lib.mjs', 'utf8');
  for (const must of ["'--exit-code', '1'", "labels['org.opencontainers.image.revision'] !== sha", "'cyclonedx'"]) assert.ok(lib.includes(must), must);
});

test('开发候选输入 fail-closed，schema 版本取自迁移文件', () => {
  const good = { GITHUB_SHA: 'e'.repeat(40), GITHUB_REPOSITORY: 'guolichen007/crane-operations-platform', GITHUB_REF_TYPE: 'tag', GITHUB_REF_NAME: 'dev/crane-d1-1', CI_RUN_ID: '42592243327' };
  assert.deepEqual(checkInputs(good), []);
  for (const [k, v] of [['GITHUB_SHA', 'e7bb8e3'], ['GITHUB_REPOSITORY', 'x/y'], ['GITHUB_REF_TYPE', 'branch'], ['GITHUB_REF_NAME', 'preview/h3-rc1'], ['GITHUB_REF_NAME', 'dev/crane-..x'], ['GITHUB_REF_NAME', 'dev/crane-RC'], ['CI_RUN_ID', '']])
    assert.ok(checkInputs({ ...good, [k]: v }).length > 0, `${k}=${v}`);
  assert.equal(schemaVersion(['00001_baseline.sql', '00012_equipment_instance_spec.sql', 'embed.go', 'governance_test.go']), 12);
});
