// G4-S1A static gate for the independent Preview deployment.
// Validates deploy/baobei/compose.preview.yaml (as rendered by
// `docker compose config` when Docker is available, otherwise by a local
// interpolation of the raw file), the Preview Nginx files and the ingress
// example, then proves with negative mutations that unsafe changes fail.
// Never starts containers.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
const require = createRequire(new URL('../../apps/project-intake-web/package.json', import.meta.url));
const { parse } = require('yaml');

const FILE = 'deploy/baobei/compose.preview.yaml';
const PG = 'postgres:18.4-alpine@sha256:9a8afca54e7861fd90fab5fdf4c42477a6b1cb7d293595148e674e0a3181de15';
const NAMES = ['baobei-preview-api', 'baobei-preview-postgres', 'baobei-preview-web'];
const PRODUCTION = /u-dao|baobei-production|^baobei-(web|api|postgres)$|crane|firebot|ingress/;
export const PREVIEW_ENV = {
  APP_ENV: 'preview',
  DEV_AUTH: '0',
  LISTEN_ADDR: '0.0.0.0:8088',
  PUBLIC_ORIGIN: 'https://baobei-preview.youdaozd.com',
  OIDC_ISSUER: 'https://crane.youdaozd.com/realms/baobei-preview',
  OIDC_CLIENT_ID: 'baobei-preview-web',
  OIDC_CLIENT_SECRET_FILE: '/run/secrets/preview_oidc_client_secret',
  DATABASE_URL_FILE: '/run/secrets/preview_runtime_database_url',
  STAFF_DATABASE_URL_FILE: '/run/secrets/preview_staff_database_url',
};
const list = (v) => (Array.isArray(v) ? v : Object.keys(v || {}));
const secretNames = (v) => (v || []).map((x) => (typeof x === 'string' ? x : x.source)).sort();

export function validatePreview(c) {
  assert.equal(c.name, 'baobei-preview', 'compose project name');
  const s = c.services;
  assert.deepEqual(Object.keys(s).sort(), NAMES, 'service names');
  for (const [name, v] of Object.entries(s)) {
    assert(!v.ports?.length, name + ' publishes a host port');
    assert(!v.expose?.length, name + ' exposes ports');
    assert(!v.network_mode && !v.container_name && !v.extends, name + ' namespace escape');
    assert(!v.privileged && !v.pid && !v.ipc && !v.cap_add?.length, name + ' privileges');
    assert.equal(v.read_only ?? (name === 'baobei-preview-postgres'), true, name + ' read_only');
    assert((v.security_opt || []).includes('no-new-privileges:true'), name + ' no-new-privileges');
    for (const n of list(v.networks)) assert(!PRODUCTION.test(n), name + ' joins ' + n);
    for (const vol of v.volumes || []) {
      const src = String(vol.source || '');
      assert(!/production|u-dao|acme|tls|secrets/.test(src), name + ' mounts ' + src);
    }
  }
  assert.deepEqual(list(s['baobei-preview-web'].networks).sort(), ['preview-frontend', 'preview-internal']);
  assert.deepEqual(list(s['baobei-preview-api'].networks).sort(), ['preview-egress', 'preview-internal']);
  assert.deepEqual(list(s['baobei-preview-postgres'].networks).sort(), ['preview-internal']);
  const nets = c.networks;
  for (const n of Object.values(nets)) assert(!PRODUCTION.test(String(n.name)), 'network ' + n.name + ' is a production/foreign network');
  assert.deepEqual(Object.keys(nets).sort(), ['preview-egress', 'preview-frontend', 'preview-internal']);
  assert.equal(nets['preview-internal'].name, 'baobei-preview-internal');
  assert.equal(nets['preview-internal'].internal, true, 'internal network must be internal');
  assert(!nets['preview-internal'].external, 'internal network must be owned by the preview project');
  assert.equal(nets['preview-frontend'].name, 'baobei-preview-frontend');
  assert.equal(nets['preview-frontend'].external, true);
  assert.equal(nets['preview-egress'].name, 'baobei-preview-egress');
  assert(!nets['preview-egress'].external);
  for (const n of Object.values(nets)) assert(!PRODUCTION.test(n.name), 'network ' + n.name);
  // Volume: one, preview-named, never external (cannot attach a production volume).
  assert.deepEqual(Object.keys(c.volumes || {}), ['baobei-preview-data']);
  const vol = c.volumes['baobei-preview-data'];
  assert.equal(vol.name, 'baobei-preview-data');
  assert(!vol.external, 'preview volume must not be external');
  assert(!s['baobei-preview-web'].volumes?.length, 'web mounts');
  assert(!s['baobei-preview-api'].volumes?.length, 'api mounts');
  const pgv = s['baobei-preview-postgres'].volumes;
  assert.equal(pgv.length, 4);
  assert.equal(pgv.filter((v) => v.type === 'volume')[0].source, 'baobei-preview-data');
  const init = pgv.filter((v) => v.type === 'bind');
  assert.deepEqual(init.map((v) => v.target).sort(), [
    '/docker-entrypoint-initdb.d/10-baobei-roles.sql',
    '/docker-entrypoint-initdb.d/20-baobei-staff-role.sql',
    '/docker-entrypoint-initdb.d/30-baobei-preview-environment.sql',
  ]);
  for (const v of init) {
    assert.equal(v.read_only, true, v.target + ' read_only');
    assert(/deploy\/baobei\/db\/(initialize|staff-role|preview-environment)\.sql$/.test(v.source.replace(/\\/g, '/')), v.source);
  }
  // API environment: exact preview identity, file secrets only.
  const env = s['baobei-preview-api'].environment;
  assert.deepEqual(Object.fromEntries(Object.entries(env).map(([k, v]) => [k, String(v)])), PREVIEW_ENV, 'api environment');
  assert.deepEqual(secretNames(s['baobei-preview-api'].secrets), ['preview_oidc_client_secret', 'preview_runtime_database_url', 'preview_staff_database_url']);
  assert.deepEqual(secretNames(s['baobei-preview-postgres'].secrets), ['preview_db_password']);
  assert(!s['baobei-preview-web'].secrets?.length);
  for (const v of Object.values(s))
    for (const k of Object.keys(v.environment || {}))
      assert(!/^(POSTGRES_PASSWORD|OIDC_CLIENT_SECRET|DATABASE_URL|STAFF_DATABASE_URL)$/.test(k), 'plaintext secret ' + k);
  const pgEnv = s['baobei-preview-postgres'].environment;
  assert.equal(pgEnv.POSTGRES_USER, 'baobei_bootstrap');
  assert.equal(pgEnv.POSTGRES_PASSWORD_FILE, '/run/secrets/preview_db_password');
  assert.deepEqual(Object.keys(c.secrets).sort(), ['preview_db_password', 'preview_oidc_client_secret', 'preview_runtime_database_url', 'preview_staff_database_url']);
  for (const [k, v] of Object.entries(c.secrets)) {
    assert(v.file && !v.external && !v.environment, k + ' must be a file secret');
    assert(/\/baobei-preview\/|^\/preview-secrets\//.test(v.file.replace(/\\/g, '/')) || v.file.includes('PREVIEW_SECRETS_TEST'), k + ' secret path ' + v.file);
    assert(!/u-dao\/secrets\/baobei\/|production/.test(v.file), k + ' uses a production secret path');
  }
  // Images: registry digests only (same rule as production) and pinned PostgreSQL.
  for (const [svc, repo] of [['baobei-preview-web', 'baobei-web'], ['baobei-preview-api', 'baobei-api']])
    assert(new RegExp('^ghcr\\.io/guolichen007/' + repo + '@sha256:[a-f0-9]{64}$').test(s[svc].image), svc + ' image must be a registry digest');
  assert.equal(s['baobei-preview-postgres'].image, PG);
  // Health checks match the images: web (nginx) and api (alpine busybox wget).
  for (const n of NAMES) assert(Array.isArray(s[n].healthcheck?.test), n + ' healthcheck required');
  assert.deepEqual(s['baobei-preview-api'].healthcheck.test, ['CMD', 'wget', '-qO-', 'http://127.0.0.1:8088/health/ready']);
  assert.deepEqual(s['baobei-preview-web'].healthcheck.test, ['CMD', 'wget', '-qO-', 'http://127.0.0.1:8080/health/ready']);
  assert.deepEqual(s['baobei-preview-postgres'].healthcheck.test, ['CMD-SHELL', 'pg_isready -U baobei_bootstrap -d baobei']);
  assert.equal(s['baobei-preview-api'].depends_on['baobei-preview-postgres'].condition, 'service_healthy');
  // Nginx configs: preview files only.
  assert.deepEqual((s['baobei-preview-web'].configs || []).map((x) => [x.source, x.target]), [
    ['nginx-preview', '/etc/nginx/conf.d/default.conf'],
    ['nginx-preview-proxy', '/etc/nginx/baobei-proxy.conf'],
  ]);
  for (const [k, v] of Object.entries(c.configs || {})) assert(/nginx\.(proxy\.)?preview\.conf$/.test(v.file.replace(/\\/g, '/')), k + ' ' + v.file);
  return true;
}

// Raw-file interpolation for environments without Docker (same variables the
// operator must provide; `:?` makes Compose refuse when they are missing).
export function renderLocally(src, vars) {
  const text = src.replace(/\$\{([A-Z_]+):\?[^}]*\}/g, (_, k) => {
    assert(k in vars, 'missing required variable ' + k);
    return vars[k];
  });
  assert(!/\$\{/.test(text), 'unresolved interpolation');
  const c = parse(text);
  for (const v of Object.values(c.services)) {
    if (Array.isArray(v.secrets)) v.secrets = v.secrets.map((x) => (typeof x === 'string' ? { source: x } : x));
    if (v.healthcheck?.test && !Array.isArray(v.healthcheck.test)) v.healthcheck.test = ['CMD-SHELL', v.healthcheck.test];
    for (const vol of v.volumes || [])
      if (typeof vol === 'string') {
        const [source, target] = vol.split(':');
        Object.assign(v.volumes[v.volumes.indexOf(vol)] = { type: 'volume', source, target });
      }
  }
  for (const vol of Object.values(c.services['baobei-preview-postgres'].volumes)) if (vol.type === 'bind') vol.source = 'deploy/baobei/' + vol.source.replace(/^\.\//, '');
  for (const cfg of Object.values(c.configs || {})) cfg.file = 'deploy/baobei/' + cfg.file.replace(/^\.\//, '');
  return c;
}

const testVars = {
  BAOBEI_PREVIEW_WEB_IMAGE: 'ghcr.io/guolichen007/baobei-web@sha256:' + 'c'.repeat(64),
  BAOBEI_PREVIEW_API_IMAGE: 'ghcr.io/guolichen007/baobei-api@sha256:' + 'd'.repeat(64),
  BAOBEI_PREVIEW_SECRETS_DIR: '/srv/u-dao/secrets/baobei-preview',
};
const src = readFileSync(FILE, 'utf8');
// Missing required variables are refused (no default image, no default secret dir).
for (const k of Object.keys(testVars)) {
  const v = { ...testVars };
  delete v[k];
  assert.throws(() => renderLocally(src, v), new RegExp(k));
}
let c = renderLocally(src, testVars);
let renderedBy = 'local interpolation';
const docker = spawnSync('docker', [...(process.env.BAOBEI_DOCKER_CONTEXT ? ['--context', process.env.BAOBEI_DOCKER_CONTEXT] : []), 'compose', '-f', FILE, 'config', '--format', 'json'], {
  encoding: 'utf8',
  env: { ...process.env, ...testVars, BAOBEI_PREVIEW_SECRETS_DIR: process.cwd() + '/local-private/baobei/PREVIEW_SECRETS_TEST/baobei-preview' },
});
if (!docker.error && docker.status === 0) {
  c = JSON.parse(docker.stdout);
  renderedBy = 'docker compose config';
} else if (process.env.CI) {
  throw Error('docker compose config failed in CI: ' + (docker.stderr || docker.error));
}
validatePreview(c);
console.log(`PREVIEW_COMPOSE_RENDERED_BY=${renderedBy}`);

// Negative mutations: every one must be rejected by the gate.
const mutations = {
  'host port on api': (x) => (x.services['baobei-preview-api'].ports = [{ target: 8088, published: '8088' }]),
  'host port on postgres': (x) => (x.services['baobei-preview-postgres'].ports = [{ target: 5432, published: '55432' }]),
  'host port on web': (x) => (x.services['baobei-preview-web'].ports = [{ target: 8080, published: '80' }]),
  'network_mode host': (x) => (x.services['baobei-preview-api'].network_mode = 'host'),
  'production project name': (x) => (x.name = 'u-dao-baobei'),
  'production service name': (x) => { x.services['baobei-api'] = x.services['baobei-preview-api']; delete x.services['baobei-preview-api']; },
  'join production internal network': (x) => { x.networks['prod'] = { name: 'u-dao-baobei-internal', external: true }; x.services['baobei-preview-api'].networks.prod = {}; },
  'preview internal renamed to production': (x) => (x.networks['preview-internal'].name = 'u-dao-baobei-internal'),
  'internal network external (attach existing)': (x) => (x.networks['preview-internal'].external = true),
  'internal network not internal': (x) => (x.networks['preview-internal'].internal = false),
  'postgres on egress': (x) => (x.services['baobei-preview-postgres'].networks['preview-egress'] = {}),
  'api on frontend': (x) => (x.services['baobei-preview-api'].networks['preview-frontend'] = {}),
  'frontend renamed to production': (x) => (x.networks['preview-frontend'].name = 'u-dao-baobei-frontend'),
  'production volume': (x) => { x.volumes = { 'baobei-production-data': { name: 'u-dao-baobei_baobei-production-data' } }; },
  'external volume': (x) => (x.volumes['baobei-preview-data'].external = true),
  'volume renamed to production': (x) => (x.volumes['baobei-preview-data'].name = 'u-dao-baobei_baobei-production-data'),
  'web mounts acme/tls': (x) => (x.services['baobei-preview-web'].volumes = [{ type: 'bind', source: '/srv/u-dao/platform/ingress/acme-webroot', target: '/var/www/acme' }]),
  'api mounts production secrets': (x) => (x.services['baobei-preview-api'].volumes = [{ type: 'bind', source: '/srv/u-dao/secrets/baobei', target: '/s' }]),
  'APP_ENV production': (x) => (x.services['baobei-preview-api'].environment.APP_ENV = 'production'),
  'APP_ENV development': (x) => (x.services['baobei-preview-api'].environment.APP_ENV = 'development'),
  'DEV_AUTH 1': (x) => (x.services['baobei-preview-api'].environment.DEV_AUTH = '1'),
  'production origin': (x) => (x.services['baobei-preview-api'].environment.PUBLIC_ORIGIN = 'https://baobei.youdaozd.com'),
  'production issuer': (x) => (x.services['baobei-preview-api'].environment.OIDC_ISSUER = 'https://crane.youdaozd.com/realms/crane'),
  'production client': (x) => (x.services['baobei-preview-api'].environment.OIDC_CLIENT_ID = 'baobei-web'),
  'plaintext client secret': (x) => (x.services['baobei-preview-api'].environment.OIDC_CLIENT_SECRET = 'x'),
  'plaintext database url': (x) => (x.services['baobei-preview-api'].environment.DATABASE_URL = 'postgres://x'),
  'missing staff DSN': (x) => { delete x.services['baobei-preview-api'].environment.STAFF_DATABASE_URL_FILE; },
  'staff DSN secret removed': (x) => (x.services['baobei-preview-api'].secrets = x.services['baobei-preview-api'].secrets.filter((s) => s.source !== 'preview_staff_database_url')),
  'migrator DSN for runtime': (x) => (x.services['baobei-preview-api'].environment.DATABASE_URL_FILE = '/run/secrets/migrator_database_url'),
  'production secret dir': (x) => (x.secrets.preview_db_password.file = '/srv/u-dao/secrets/baobei/db_password'),
  'external secret': (x) => (x.secrets.preview_db_password = { external: true }),
  'tag image': (x) => (x.services['baobei-preview-api'].image = 'ghcr.io/guolichen007/baobei-api:latest'),
  'foreign registry image': (x) => (x.services['baobei-preview-web'].image = 'ghcr.io/other/baobei-web@sha256:' + 'a'.repeat(64)),
  'unpinned postgres': (x) => (x.services['baobei-preview-postgres'].image = 'postgres:18.4-alpine'),
  'missing preview marker init': (x) => (x.services['baobei-preview-postgres'].volumes = x.services['baobei-preview-postgres'].volumes.filter((v) => !String(v.target).includes('preview-environment'))),
  'writable init script': (x) => (x.services['baobei-preview-postgres'].volumes.find((v) => v.type === 'bind').read_only = false),
  'api healthcheck removed': (x) => { delete x.services['baobei-preview-api'].healthcheck; },
  'api healthcheck wrong port': (x) => (x.services['baobei-preview-api'].healthcheck.test = ['CMD', 'wget', '-qO-', 'http://127.0.0.1:8080/health/ready']),
  'production nginx config': (x) => (x.configs['nginx-preview'].file = 'deploy/baobei/nginx.production.conf'),
  'privileged': (x) => (x.services['baobei-preview-web'].privileged = true),
  'writable root fs': (x) => (x.services['baobei-preview-api'].read_only = false),
  'container_name': (x) => (x.services['baobei-preview-web'].container_name = 'baobei-web'),
};
let rejected = 0;
for (const [label, mutate] of Object.entries(mutations)) {
  const x = structuredClone(c);
  mutate(x);
  assert.throws(() => validatePreview(x), undefined, 'negative not rejected: ' + label);
  rejected++;
}
console.log(`PREVIEW_COMPOSE_NEGATIVES_REJECTED=${rejected}/${Object.keys(mutations).length}`);

// Production and Preview gates never accept each other's configuration.
const { validateProduction } = await import('./production-gate.mjs');
assert.throws(() => validateProduction(c), undefined, 'production gate accepted the preview compose');
const prodRaw = readFileSync('deploy/baobei/compose.production.yaml', 'utf8');
const prod = parse(prodRaw.replace(/\$\{BAOBEI_WEB_IMAGE:\?[^}]*\}/, 'ghcr.io/guolichen007/baobei-web@sha256:' + 'a'.repeat(64)).replace(/\$\{BAOBEI_API_IMAGE:\?[^}]*\}/, 'ghcr.io/guolichen007/baobei-api@sha256:' + 'b'.repeat(64)).replace(/\$\{BAOBEI_SECRETS_DIR:\?[^}]*\}/g, '/srv/u-dao/secrets/baobei'));
assert.throws(() => validatePreview(prod), undefined, 'preview gate accepted the production compose');
assert(!/preview/i.test(prodRaw), 'production compose references preview');
console.log('PREVIEW_PRODUCTION_CROSS_REJECTED=YES');

// Nginx: preview names only, no production upstream, server name or zones.
const web = readFileSync('deploy/baobei/nginx.preview.conf', 'utf8');
assert(/server_name baobei-preview\.youdaozd\.com;/.test(web));
assert(web.includes('set $baobei_api http://baobei-preview-api:8088;'));
assert(!/baobei\.youdaozd\.com|http:\/\/baobei-api|zone=baobei_(login|callback)_global|acme/.test(web.replace(/baobei-preview\.youdaozd\.com/g, '')), 'production names in nginx.preview.conf');
const lf = (f) => readFileSync(f, 'utf8').replace(/\r\n/g, '\n');
assert.equal(lf('deploy/baobei/nginx.proxy.preview.conf'), lf('deploy/baobei/nginx.proxy.production.conf'), 'proxy header policy must equal production');
const ing = readFileSync('deploy/baobei/ingress/baobei-preview.youdaozd.com.conf.example', 'utf8');
assert(/server_name baobei-preview\.youdaozd\.com;/.test(ing) && ing.includes('http://baobei-preview-web:8080'));
assert(ing.includes('/etc/nginx/tls/baobei-preview/') && /deny all;/.test(ing));
assert(!/zone=baobei_ingress_(login|callback)\b|http:\/\/baobei-web:|\/tls\/baobei\/|server_name baobei\.youdaozd\.com/.test(ing), 'production names in preview ingress');
console.log('PREVIEW_NGINX_NAMES_ISOLATED=YES');

// direct-deploy.py stays production-only and never references preview files.
const dd = readFileSync('deploy/baobei/direct-deploy.py', 'utf8');
assert(!/preview/i.test(dd), 'direct-deploy must not deploy preview');
console.log('Preview topology, isolation, secret-file, digest, OIDC and negative gates PASS');

const nginxTest = spawnSync('docker', [...(process.env.BAOBEI_DOCKER_CONTEXT ? ['--context', process.env.BAOBEI_DOCKER_CONTEXT] : []), 'run', '--rm', '--entrypoint', 'nginx',
  '-v', process.cwd() + '/deploy/baobei/nginx.preview.conf:/etc/nginx/conf.d/default.conf:ro',
  '-v', process.cwd() + '/deploy/baobei/nginx.proxy.preview.conf:/etc/nginx/baobei-proxy.conf:ro',
  'nginx:1.30.4-alpine', '-t'], { encoding: 'utf8' });
if (!nginxTest.error && nginxTest.status !== null) {
  assert.equal(nginxTest.status, 0, 'Preview Nginx syntax validation failed: ' + nginxTest.stderr);
  console.log('Preview nginx syntax PASS');
} else if (process.env.CI) {
  throw Error('nginx syntax check needs Docker in CI');
} else console.log('Preview nginx syntax NOT_RUN (no Docker locally; runs in CI compose job)');
