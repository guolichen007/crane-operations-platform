// Production compose validator (pure; no side effects). Shared by
// tests/baobei/production.mjs (the production gate) and tests/baobei/preview.mjs
// (cross-check: the production gate must reject the Preview configuration).
// Moved verbatim from production.mjs in G4-S1A; rules unchanged.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
const require = createRequire(new URL('../../apps/project-intake-web/package.json', import.meta.url));
const { parse } = require('yaml');
const raw = parse(readFileSync('deploy/baobei/compose.production.yaml', 'utf8'));
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
  assert.equal(s['baobei-postgres'].volumes.length, 3);
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
  assert.equal(env.STAFF_DATABASE_URL_FILE, '/run/secrets/staff_database_url');
  assert.equal(env.DATABASE_URL_FILE, '/run/secrets/runtime_database_url');
  assert.deepEqual(s['baobei-api'].secrets.map(secret => secret.source).sort(),
    ['oidc_client_secret', 'runtime_database_url', 'staff_database_url']);
  assert.equal(s['baobei-postgres'].environment.POSTGRES_USER, 'baobei_bootstrap');
  assert(s['baobei-postgres'].volumes.some(volume =>
    volume.target === '/docker-entrypoint-initdb.d/10-baobei-roles.sql' && volume.read_only));
  for (const name of ['baobei-web', 'baobei-api'])
    assert(new RegExp('^ghcr\\.io/guolichen007/' + name + '@sha256:[a-f0-9]{64}$').test(s[name].image), 'fixed GHCR registry digest required');
  assert.equal(s['baobei-postgres'].image,'postgres:18.4-alpine@sha256:9a8afca54e7861fd90fab5fdf4c42477a6b1cb7d293595148e674e0a3181de15');
  return true;
}
