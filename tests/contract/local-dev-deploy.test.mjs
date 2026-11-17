// Source-direct crane-dev deployment (deploy/scripts/local-dev-deploy.mjs) with a fake Docker:
// full-SHA local tags, no overwrite of an existing SHA with different content, no pull, running
// image ID verified, only crane-dev, rollback recorded; production validation unchanged.
import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { build, plan, apply, rollback, localTag, overrideYaml, validateLocalDevelopment, changedServices, BUILDABLE } from '../../deploy/scripts/local-dev-deploy.mjs';
import { validateProduction } from '../../tools/production-contract.mjs';

const UP = JSON.parse(readFileSync('deploy/production/images.json', 'utf8'));

function repo(t) {
  const dir = mkdtempSync(join(tmpdir(), 'crane-local-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const root = join(dir, 'checkout'); mkdirSync(root);
  const git = (...a) => { const r = spawnSync('git', ['-C', root, '-c', 'user.name=A', '-c', 'user.email=a@example.test', '-c', 'commit.gpgsign=false', ...a], { encoding: 'utf8' }); assert.equal(r.status, 0, r.stderr); return r.stdout.trim(); };
  git('init', '-q');
  for (const f of ['deploy/compose/docker-compose.prod.yml', 'deploy/compose/docker-compose.crane-dev.yml', 'deploy/postgres/migrations/00012_x.sql', 'apps/web-console/a.ts', 'internal/a.go', 'go.mod']) { mkdirSync(join(root, f, '..'), { recursive: true }); writeFileSync(join(root, f), 'x\n'); }
  git('add', '.'); git('commit', '-qm', 'one');
  const env = join(dir, 'crane.env'); writeFileSync(env, `CRANE_LOCAL_DEPLOY_DIR=${join(dir, 'state')}\n`, { mode: 0o600 });
  return { dir, root, git, env, sha: () => git('rev-parse', 'HEAD') };
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

/** Fake docker: images keyed by tag; containers run what `up` was given; records every call. */
function fakeDocker(r, { buildId = (tag) => 'sha256:' + Buffer.from(tag).toString('hex').padEnd(64, '0').slice(0, 64), arch = 'amd64' } = {}) {
  const state = { images: {}, running: {}, calls: [], schema: 12, override: null };
  const execute = (cmd, args, opts) => {
    if (cmd === 'git') return spawnSync(cmd, args, { ...opts, encoding: 'utf8' });
    state.calls.push(args.join(' '));
    if (args[0] === 'build') {
      const tag = args[args.indexOf('-t') + 1];
      const shaArg = args[args.indexOf('--build-arg') + 1].split('=')[1];
      const svc = tag.split('/')[1].split(':')[0];
      state.images[tag] = { Id: buildId(svc + shaArg), Config: { Labels: { 'org.opencontainers.image.revision': shaArg } }, Os: 'linux', Architecture: arch };
      return { status: 0, stdout: '' };
    }
    if (args[0] === 'image' && args[1] === 'inspect') { const v = state.images[args.at(-1)]; return v ? { status: 0, stdout: JSON.stringify(v) } : { status: 1, stdout: '' }; }
    if (args[0] === 'image' && args[1] === 'rm') { delete state.images[args[2]]; return { status: 0, stdout: '' }; }
    if (args[0] === 'tag') { state.images[args[2]] = state.images[args[1]]; return { status: 0, stdout: '' }; }
    if (args[0] === 'push' || (args.includes('pull') && !args.includes('never'))) throw Error('network/registry access attempted: ' + args.join(' '));
    if (args[0] === 'compose') {
      const ov = args[args.lastIndexOf('-f') + 1];
      if (/override-/.test(ov)) state.override = readFileSync(ov, 'utf8');
      if (args.includes('psql')) return { status: 0, stdout: state.schema + '\n' };
      const imgs = Object.fromEntries([...state.override.matchAll(/  (cloud-api|web-console):\n    image: (\S+)/g)].map((m) => [m[1], m[2]]));
      if (args.includes('config')) return { status: 0, stdout: JSON.stringify(devConfig(imgs)) };
      if (args.includes('up')) { assert.ok(args.includes('--no-build') && args.includes('never')); for (const s of ['cloud-api', 'web-console']) state.running[s] = imgs[s]; return { status: 0, stdout: '' }; }
      if (args.includes('ps')) return { status: 0, stdout: 'c-' + args.at(-1) + '\n' };
      if (args.includes('psql')) return { status: 0, stdout: state.schema + '\n' };
    }
    if (args[0] === 'inspect') {
      const svc = args.at(-1).slice(2); const tag = state.running[svc]; const v = state.images[tag];
      if (args[2] === '{{.Image}}') return { status: 0, stdout: v.Id + '\n' };
      if (args[2] === '{{.Config.Image}}') return { status: 0, stdout: tag + '\n' };
      return { status: 0, stdout: v.Config.Labels['org.opencontainers.image.revision'] + '\n' };
    }
    throw Error('unexpected docker ' + args.join(' '));
  };
  return { state, opts: { root: r.root, execute, probe: (u) => u === 'https://crane.youdaozd.com/' ? { status: 200, body: '' } : u.includes('openid') ? { status: 200, body: JSON.stringify({ issuer: 'https://crane.youdaozd.com/realms/crane' }) } : u.includes('/api/') ? { status: 401 } : { status: 404 }, log: () => {}, now: () => new Date() } };
}

test('build: full-SHA local tags, revision label checked, nothing pushed, existing SHA never overwritten with other content', (t) => {
  const r = repo(t); const d = fakeDocker(r);
  const out = build(r.env, ['cloud-api', 'web-console', 'device-simulator'], d.opts);
  for (const s of ['cloud-api', 'web-console', 'device-simulator']) { assert.equal(out.images[s].tag, localTag(s, r.sha())); assert.equal(out.images[s].platform, 'linux/amd64'); }
  assert.ok(!d.state.calls.some((c) => /^push\b|(^| )pull( |$)/.test(c)));
  assert.ok(d.state.calls.filter((c) => c.startsWith('build')).every((c) => c.includes('--pull=false')));
  assert.equal(build(r.env, ['cloud-api'], d.opts).images['cloud-api'].reused, true);
  let n = 0; const drift = fakeDocker(r, { buildId: () => 'sha256:' + String(++n).padStart(64, '0') }); drift.state.images = d.state.images;
  assert.throws(() => build(r.env, ['cloud-api'], drift.opts), /different content/);
  writeFileSync(join(r.root, 'dirty.txt'), 'x');
  assert.throws(() => build(r.env, ['cloud-api'], d.opts), /clean Git worktree/);
});

test('apply: local images, never pulled, running image ID verified, previous + rollback recorded; rollback returns', (t) => {
  const r = repo(t); const d = fakeDocker(r);
  build(r.env, ['cloud-api', 'web-console'], d.opts);
  const first = apply(r.env, undefined, d.opts);
  assert.equal(first.images['cloud-api'].tag, localTag('cloud-api', r.sha()));
  assert.match(d.state.override, /pull_policy: never/);
  assert.doesNotMatch(d.state.override, /environment|ports|volumes|secrets|networks/);
  const one = r.sha();
  writeFileSync(join(r.root, 'apps/web-console/a.ts'), 'y\n'); r.git('commit', '-qam', 'web only');
  build(r.env, ['web-console'], d.opts);
  const p = plan(r.env, d.opts);
  assert.equal(p.images['cloud-api'].carried, true, 'unchanged cloud-api keeps the running image');
  assert.equal(p.images['web-console'].tag, localTag('web-console', r.sha()));
  const second = apply(r.env, undefined, d.opts);
  assert.match(second.rollback, new RegExp(one));
  assert.deepEqual(changedServices({ root: r.root, execute: d.opts.execute }, one, r.sha(), Object.keys(BUILDABLE)), ['web-console']);
  r.git('checkout', '-q', one);
  rollback(r.env, d.opts);
  assert.equal(d.state.running['web-console'], localTag('web-console', one));
  // a running image that is not the built one is detected
  r.git('checkout', '-q', '-');
  const bad = fakeDocker(r); bad.state.images = d.state.images; bad.state.running = d.state.running;
  const orig = bad.opts.execute; bad.opts.execute = (c, a, o) => (c === 'docker' && a[0] === 'inspect' && a[2] === '{{.Image}}') ? { status: 0, stdout: 'sha256:' + 'f'.repeat(64) + '\n' } : orig(c, a, o);
  assert.throws(() => apply(r.env, undefined, bad.opts), /is not the built/);
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
