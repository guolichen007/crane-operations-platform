import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
const require = createRequire(
  new URL('../../apps/project-intake-web/package.json', import.meta.url)
);
const { parse } = require('yaml');
const src = readFileSync('deploy/baobei/compose.production.yaml', 'utf8');
const raw = parse(src);
export function validateProduction(c) {
  const s = c.services;
  assert.deepEqual(Object.keys(s).sort(), ['baobei-api', 'baobei-postgres', 'baobei-web']);
  for (const [name, v] of Object.entries(s)) {
    assert(!v.ports?.length, name + ' host port');
    assert(!v.network_mode && !v.container_name, name + ' namespace escape');
    assert(!v.privileged && !v.pid && !v.cap_add?.includes('NET_ADMIN'));
    for (const n of Object.keys(v.networks || {})) {
      assert(!/crane|firebot|ingress/.test(n), n);
    }
  }
  const keys = (name) => Object.keys(s[name].networks).sort();
  assert.deepEqual(keys('baobei-web'), ['frontend', 'internal']);
  assert.deepEqual(keys('baobei-api'), ['egress', 'internal']);
  assert.deepEqual(keys('baobei-postgres'), ['internal']);
  assert.equal(c.networks.internal.internal, true);assert.equal(c.networks.internal.name,'u-dao-baobei-internal');assert.equal(c.networks.frontend.external,true);
  assert.equal(c.networks.frontend.name, 'u-dao-baobei-frontend');
  assert.equal(c.networks.egress.name, 'u-dao-baobei-egress');
  assert(!c.networks.egress.internal);
  const web = s['baobei-web'];
  assert.equal(web.volumes.length, 1);
  const {bind, ...mount} = web.volumes[0];
  assert.deepEqual(mount, {
    type: 'bind',
    source: '/srv/u-dao/platform/ingress/acme-webroot',
    target: '/var/www/acme',
    read_only: true,
  });
  // Compose versions differ in whether config JSON emits false-valued bind options.
  assert.equal(raw.services['baobei-web'].volumes[0].bind.create_host_path, false);
  assert.equal(bind?.create_host_path ?? false, false);
  assert.deepEqual(web.configs.map(({source, target}) => [source, target]), [
    ['nginx-production', '/etc/nginx/conf.d/default.conf'],
    ['nginx-proxy', '/etc/nginx/baobei-proxy.conf'],
  ]);
  assert(!web.secrets?.length);
  assert(!s['baobei-api'].volumes?.length);
  assert.equal(s['baobei-postgres'].volumes.length, 2);
  assert(!s['baobei-postgres'].volumes.some(volume =>
    volume.target === '/var/www/acme' || volume.source?.endsWith('/acme-webroot')));
  const env = s['baobei-api'].environment;
  assert.equal(env.APP_ENV, 'production');
  assert.equal(String(env.DEV_AUTH), '0');
  assert.equal(env.PUBLIC_ORIGIN, 'https://baobei.youdaozd.com');
  assert.equal(env.OIDC_CLIENT_ID, 'baobei-web');
  assert.equal(env.OIDC_ISSUER, 'https://crane.youdaozd.com/realms/crane');
  assert(env.OIDC_CLIENT_SECRET_FILE && !env.OIDC_CLIENT_SECRET);
  assert(!env.DATABASE_URL);
  assert.equal(env.DATABASE_URL_FILE, '/run/secrets/runtime_database_url');
  assert.deepEqual(s['baobei-api'].secrets.map(secret => secret.source).sort(),
    ['oidc_client_secret', 'runtime_database_url']);
  assert.equal(s['baobei-postgres'].environment.POSTGRES_USER, 'baobei_bootstrap');
  assert(s['baobei-postgres'].volumes.some(volume =>
    volume.target === '/docker-entrypoint-initdb.d/10-baobei-roles.sql' && volume.read_only));
  for (const name of ['baobei-web', 'baobei-api'])
    assert(new RegExp('^ghcr\\.io/guolichen007/' + name + '@sha256:[a-f0-9]{64}$').test(s[name].image), 'fixed GHCR registry digest required');
  assert.equal(s['baobei-postgres'].image,'postgres:18.4-alpine@sha256:9a8afca54e7861fd90fab5fdf4c42477a6b1cb7d293595148e674e0a3181de15');
  return true;
}
const r = spawnSync(
  'docker',
  [
    ...(process.env.BAOBEI_DOCKER_CONTEXT ? ['--context', process.env.BAOBEI_DOCKER_CONTEXT] : []),
    'compose',
    '-f',
    'deploy/baobei/compose.production.yaml',
    'config',
    '--format',
    'json',
  ],
  {
    encoding: 'utf8',
    env: {
      ...process.env,
      BAOBEI_WEB_IMAGE: 'ghcr.io/guolichen007/baobei-web@sha256:' + 'a'.repeat(64),
      BAOBEI_API_IMAGE: 'ghcr.io/guolichen007/baobei-api@sha256:' + 'b'.repeat(64),
      BAOBEI_SECRETS_DIR: process.cwd() + '/local-private/baobei/config-test-only',
    },
  }
);
if (r.status) throw Error(r.stderr);
const c = JSON.parse(r.stdout);
validateProduction(c);
const omittedDefault = structuredClone(c);
omittedDefault.services['baobei-web'].volumes[0].bind = {};
validateProduction(omittedDefault);
for (const mutate of [
  (x) => { x.services['baobei-web'].volumes[0].read_only = false; },
  (x) => { x.services['baobei-web'].volumes[0].bind = {create_host_path: true}; },
  (x) => { x.services['baobei-web'].volumes.push({source: '/tls', target: '/etc/nginx/tls'}); },
  (x) => { x.services['baobei-web'].secrets = [{source: 'tls_private_key'}]; },
  (x) => { x.services['baobei-api'].volumes = x.services['baobei-web'].volumes; },
  (x) => { x.services['baobei-postgres'].volumes.push(...x.services['baobei-web'].volumes); },
  (x) => (x.services['baobei-api'].environment.DATABASE_URL_FILE = '/run/secrets/migrator_database_url'),
  (x) => (x.services['baobei-api'].secrets.push({source:'maintenance_database_url'})),
  (x) => (x.services['baobei-api'].ports = [{ target: 8088, published: '8088' }]),
  (x) => (x.services['baobei-web'].networks.crane = {}),
  (x) => (x.services['baobei-postgres'].networks.egress = {}),
  (x) => (x.services['baobei-api'].environment.DEV_AUTH = '1'),
  (x) => (x.services['baobei-api'].environment.PUBLIC_ORIGIN = 'http://bad'),
  (x) => (x.networks.internal.internal = false),
 (x)=>x.networks.internal.name='u-dao-crane-frontend',
 (x)=>x.networks.egress.name='firebot-internal',
]) {
  const x = structuredClone(c);
  mutate(x);
  assert.throws(() => validateProduction(x));
}
for (const service of Object.values(raw.services)) {
  for (const key of ['POSTGRES_PASSWORD', 'OIDC_CLIENT_SECRET', 'DATABASE_URL'])
    assert(!(key in (service.environment || {})), 'plaintext environment secret');
}
for(const name of ['baobei-web','baobei-api']){
 for(const image of [name+':latest',name+'@sha256:'+'a'.repeat(64),'sha256:'+'a'.repeat(64),'ghcr.io/other/'+name+'@sha256:'+'a'.repeat(64)]){
  const bad=structuredClone(c);bad.services[name].image=image;assert.throws(()=>validateProduction(bad));
 }
}
{const bad=structuredClone(c);bad.services['baobei-postgres'].image='postgres:18.4-alpine';assert.throws(()=>validateProduction(bad));}
console.log('WEB_IMAGE_REQUIRES_REGISTRY_DIGEST=YES API_IMAGE_REQUIRES_REGISTRY_DIGEST=YES PRODUCTION_POSTGRES_PINNED=YES');
const nginx = readFileSync('deploy/baobei/nginx.production.conf', 'utf8');
assert(nginx.includes('resolver 127.0.0.11'));
assert(!nginx.includes('127.0.0.1:8088'));
console.log('BAOBEI_WEB_ACME_MOUNT_READ_ONLY=YES BAOBEI_WEB_NO_TLS_PRIVATE_KEY_MOUNT=YES');
console.log('Production topology, secret-file, digest, OIDC and negative isolation gates PASS');

const syntax = spawnSync(
  'docker',
  [
    ...(process.env.BAOBEI_DOCKER_CONTEXT ? ['--context', process.env.BAOBEI_DOCKER_CONTEXT] : []),
    'run',
    '--rm',
    '--entrypoint',
    'nginx',
    '-v',
    process.cwd() + '/deploy/baobei/nginx.production.conf:/etc/nginx/conf.d/default.conf:ro',
    '-v',
    process.cwd() + '/deploy/baobei/nginx.proxy.production.conf:/etc/nginx/baobei-proxy.conf:ro',
    'nginx:1.30.4-alpine',
    '-t',
  ],
  { encoding: 'utf8' }
);
assert.equal(syntax.status, 0, 'Production Nginx syntax validation failed');
console.log('Production nginx syntax PASS');
