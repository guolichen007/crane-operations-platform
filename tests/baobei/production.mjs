import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { validateProduction } from './production-gate.mjs';
const require = createRequire(
  new URL('../../apps/project-intake-web/package.json', import.meta.url)
);
const { parse } = require('yaml');
const src = readFileSync('deploy/baobei/compose.production.yaml', 'utf8');
const raw = parse(src);
export { validateProduction };
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
// G4-S5-R1-E: the dedicated-realm overlay renders a valid production config, and
// mixed configurations (issuer without realm, account service on the shared realm,
// arbitrary issuer) are refused.
const realmRender = spawnSync('docker', [
  ...(process.env.BAOBEI_DOCKER_CONTEXT ? ['--context', process.env.BAOBEI_DOCKER_CONTEXT] : []),
  'compose', '-f', 'deploy/baobei/compose.production.yaml', '-f', 'deploy/baobei/compose.realm-baobei.yaml', 'config', '--format', 'json',
], { encoding: 'utf8', env: { ...process.env,
  BAOBEI_WEB_IMAGE: 'ghcr.io/guolichen007/baobei-web@sha256:' + 'a'.repeat(64),
  BAOBEI_API_IMAGE: 'ghcr.io/guolichen007/baobei-api@sha256:' + 'b'.repeat(64),
  BAOBEI_SECRETS_DIR: process.cwd() + '/local-private/baobei/config-test-only' } });
if (realmRender.status) throw Error(realmRender.stderr);
const dedicated = JSON.parse(realmRender.stdout);
validateProduction(dedicated);
for (const mutate of [
  (x) => (x.services['baobei-api'].environment.BAOBEI_IDENTITY_REALM = 'crane'),
  (x) => (x.services['baobei-api'].environment.OIDC_ISSUER = 'https://crane.youdaozd.com/realms/crane'),
  (x) => (x.services['baobei-api'].environment.OIDC_ISSUER = 'https://evil.example/realms/baobei'),
  (x) => (x.services['baobei-api'].environment.BAOBEI_IDENTITY_REALM = 'master'),
  (x) => { delete x.services['baobei-api'].environment.BAOBEI_ACCOUNT_CLIENT_ID; },
  (x) => (x.services['baobei-api'].environment.BAOBEI_ACCOUNT_CLIENT_SECRET = 'plaintext'),
]) {
  const x = structuredClone(dedicated);
  mutate(x);
  assert.throws(() => validateProduction(x));
}
{ const x = structuredClone(c); x.services['baobei-api'].environment.BAOBEI_ACCOUNT_CLIENT_ID = 'baobei-account-service'; assert.throws(() => validateProduction(x)); }
console.log('BAOBEI_REALM_SELECTION=APPROVED_ONLY ACCOUNT_SERVICE_DEDICATED_REALM_ONLY=YES');
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
