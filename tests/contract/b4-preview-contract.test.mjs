import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import YAML from 'yaml';
import { checkOverlay, checkResolved, PREVIEW } from '../../tools/b4-preview-contract.mjs';

const overlay = () => YAML.parse(readFileSync('deploy/compose/docker-compose.b4-preview.yml', 'utf8'));
const envExample = readFileSync('deploy/compose/b4-preview.env.example', 'utf8');

test('B4 Preview 源配置通过静态契约', () => {
  assert.deepEqual(checkOverlay(overlay(), envExample), []);
});

test('B4 Preview 源配置漂移必须被拒绝', () => {
  const productionRealm = overlay();
  productionRealm.services['cloud-api'].environment.OIDC_ISSUER = 'https://b4.crane.youdaozd.com/realms/crane';
  assert.ok(checkOverlay(productionRealm).some((e) => e.includes('realm')));

  const secondIngress = overlay();
  secondIngress.services.ingress = { image: 'nginx' };
  assert.ok(checkOverlay(secondIngress).some((e) => e.includes('ingress')));

  const productionVolume = overlay();
  productionVolume.services.postgres.volumes = ['crane-postgres:/var/lib/postgresql/data'];
  assert.ok(checkOverlay(productionVolume).some((e) => e.includes('crane-postgres')));

  const noApiFrontend = overlay();
  noApiFrontend.services['cloud-api'].networks = ['backend', 'frontend'];
  assert.ok(checkOverlay(noApiFrontend).some((e) => e.includes('api-frontend')));

  assert.ok(checkOverlay(overlay(), envExample.replace('CRANE_AUTH_PORT=18085', 'CRANE_AUTH_PORT=18084')).some((e) => e.includes('ports')));
  assert.ok(checkOverlay(overlay(), envExample.replace('CRANE_SECRET_DIR=/srv/u-dao/secrets/crane-b4-preview', 'CRANE_SECRET_DIR=/srv/u-dao/secrets/crane-ops')).length > 0);

  // G1.2-C static counter-proofs.
  const mutate = (fn) => { const o = overlay(); fn(o); return checkOverlay(o); };
  assert.ok(mutate((o) => { o.services['cloud-api'].environment.OIDC_CLIENT_ID = 'crane-bff'; }).length > 0, 'production client id');
  assert.ok(mutate((o) => { delete o.services['cloud-api'].environment.CRANE_DEPLOYMENT_ENVIRONMENT; }).length > 0, 'missing sentinel');
  assert.ok(mutate((o) => { o.services.keycloak.ports = ['127.0.0.1:18082:8080']; }).length > 0, 'production auth port');
  assert.ok(mutate((o) => { o.secrets.app_db_password.file = '/srv/u-dao/secrets/crane-ops/app_db_password'; }).length > 0, 'production secret default');
  assert.ok(mutate((o) => { delete o.volumes['b4-preview-mqtt']; }).length > 0, 'missing preview volume');
  assert.ok(mutate((o) => { delete o.networks['api-frontend']; }).length > 0, 'missing api-frontend network');
  assert.ok(checkOverlay(overlay(), envExample.replace(/CRANE_CLOUD_IMAGE=.*/, 'CRANE_CLOUD_IMAGE=ghcr.io/x/cloud-api:latest')).length > 0, 'env image not a digest');
  assert.ok(checkOverlay(overlay(), envExample.replace(/CRANE_PUBLIC_URL=.*/, 'CRANE_PUBLIC_URL=https://crane.youdaozd.com')).length > 0, 'env production origin');
});

const digest = (c) => `ghcr.io/x/${c}@sha256:${'a'.repeat(64)}`;
const REPO = '/srv/u-dao/platform/crane-operations-platform';
// A faithful resolved config, shaped like `docker compose config --format json`
// output for prod.yml + b4-preview.yml with the Preview env file.
function resolvedConfig() {
  const port = (published) => ({ mode: 'ingress', host_ip: '127.0.0.1', target: 8080, published, protocol: 'tcp' });
  const bind = (source, target, read_only = true) => ({ type: 'bind', source, target, read_only });
  const named = (source, target) => ({ type: 'volume', source, target });
  const secretRefs = (...names) => names.map((source) => ({ source, target: `/run/secrets/${source}` }));
  const secretFile = (n) => ({ name: `${PREVIEW.project}_${n}`, file: `${PREVIEW.secretRoot}/${n}` });
  return {
    name: PREVIEW.project,
    services: {
      postgres: {
        image: 'postgres:17.8-alpine@sha256:' + 'c'.repeat(64), networks: { backend: null },
        volumes: [named('b4-preview-postgres', '/var/lib/postgresql/data'), bind(REPO + '/deploy/production/postgres-entrypoint.sh', '/opt/crane/postgres-entrypoint.sh'), bind(REPO + '/deploy/production/postgres-init.sh', '/docker-entrypoint-initdb.d/10-crane-roles.sh')],
        secrets: secretRefs('postgres_admin_password', 'app_db_password', 'keycloak_db_password'),
      },
      keycloak: {
        image: 'quay.io/keycloak/keycloak:26.8.1@sha256:' + 'b'.repeat(64), networks: { backend: null, frontend: null },
        ports: [port(PREVIEW.authPort)],
        volumes: [bind(REPO + '/deploy/production/keycloak-entrypoint.sh', '/opt/crane/keycloak-entrypoint.sh'), bind(REPO + '/deploy/keycloak/themes/u-dao', '/opt/keycloak/themes/u-dao')],
        secrets: secretRefs('keycloak_db_password', 'keycloak_bootstrap_password'),
      },
      mosquitto: {
        image: 'eclipse-mosquitto:2.0.22@sha256:' + 'd'.repeat(64), networks: { backend: null },
        volumes: [bind(REPO + '/deploy/mosquitto/mosquitto.prod.conf', '/mosquitto/config/mosquitto.conf'), bind(PREVIEW.secretRoot + '/mqtt/broker', '/mosquitto/certs'), bind(PREVIEW.secretRoot + '/acl', '/mosquitto/acl'), named('b4-preview-mqtt', '/mosquitto/data')],
      },
      'cloud-api': {
        image: digest('cloud-api'), networks: { backend: null, frontend: null, 'api-frontend': null },
        environment: { OIDC_ISSUER: `${PREVIEW.origin}/realms/${PREVIEW.realm}`, PUBLIC_URL: PREVIEW.origin, CRANE_DEPLOYMENT_ENVIRONMENT: PREVIEW.environment },
        volumes: [bind(PREVIEW.secretRoot + '/inventory.json', '/inventory/inventory.json'), bind(PREVIEW.secretRoot + '/fleet.json', '/inventory/fleet.json'), bind(PREVIEW.secretRoot + '/mqtt/cloud', '/certs')],
        secrets: secretRefs('app_db_password', 'oidc_client_secret', 'session_server_secret'),
      },
      'web-console': { image: digest('web-console'), networks: { frontend: null }, ports: [port(PREVIEW.webPort)], volumes: [{ type: 'tmpfs', target: '/tmp' }] },
    },
    networks: {
      backend: { name: `${PREVIEW.project}_backend`, internal: true },
      frontend: { name: PREVIEW.networks[0], external: true },
      'api-frontend': { name: PREVIEW.networks[1], external: true },
    },
    volumes: { 'b4-preview-postgres': { name: `${PREVIEW.project}_b4-preview-postgres` }, 'b4-preview-mqtt': { name: `${PREVIEW.project}_b4-preview-mqtt` } },
    secrets: Object.fromEntries(['postgres_admin_password', 'app_db_password', 'keycloak_db_password', 'keycloak_bootstrap_password', 'oidc_client_secret', 'session_server_secret'].map((n) => [n, secretFile(n)])),
  };
}

test('B4 Preview resolved 配置通过契约', () => {
  assert.deepEqual(checkResolved(resolvedConfig()), []);
});

// The baseline above yields zero errors, so any error a mutation produces is
// caused by that mutation.
test('B4 Preview resolved 配置漂移必须被拒绝', () => {
  const cases = {
    'wrong project': (c) => { c.name = 'u-dao-crane-ops'; },
    'latest image': (c) => { c.services['cloud-api'].image = 'ghcr.io/x/cloud-api:latest'; },
    'tag not digest': (c) => { c.services['web-console'].image = 'ghcr.io/x/web-console:v1'; },
    'public bind': (c) => { c.services['web-console'].ports[0].host_ip = '0.0.0.0'; },
    'production issuer': (c) => { c.services['cloud-api'].environment.OIDC_ISSUER = 'https://crane.youdaozd.com/realms/crane'; },
    'sentinel missing': (c) => { delete c.services['cloud-api'].environment.CRANE_DEPLOYMENT_ENVIRONMENT; },
    'port collision': (c) => { c.services.keycloak.ports[0].published = PREVIEW.webPort; },
    'production network': (c) => { c.networks.frontend.name = 'u-dao-crane-frontend'; },
    'production secret': (c) => { c.secrets.app_db_password.file = '/srv/u-dao/secrets/crane-ops/app_db_password'; },
    'production volume': (c) => { c.volumes['b4-preview-postgres'].name = 'crane-postgres'; },
    // G1.2-C additions
    'extra ingress service': (c) => { c.services.ingress = { image: 'nginx:1.28-alpine@sha256:' + 'e'.repeat(64), networks: { frontend: null }, ports: [{ host_ip: '127.0.0.1', published: '18443', target: 443 }] }; },
    'extra unknown service': (c) => { c.services.debug = { image: 'busybox@sha256:' + 'f'.repeat(64), networks: { backend: null } }; },
    'missing service': (c) => { delete c.services.mosquitto; },
    'postgres on frontend': (c) => { c.services.postgres.networks.frontend = null; },
    'web-console on backend': (c) => { c.services['web-console'].networks.backend = null; },
    'cloud-api off api-frontend': (c) => { delete c.services['cloud-api'].networks['api-frontend']; },
    'unexpected cloud-api port': (c) => { c.services['cloud-api'].ports = [{ host_ip: '127.0.0.1', published: '18090', target: 8081 }]; },
    'extra postgres port': (c) => { c.services.postgres.ports = [{ host_ip: '127.0.0.1', published: '5432', target: 5432 }]; },
    'unknown network': (c) => { c.networks.extra = { name: 'shared-net', external: true }; },
    'backend not internal': (c) => { c.networks.backend.internal = false; },
    'frontend not external': (c) => { c.networks.frontend.external = false; },
    'unknown named volume': (c) => { c.volumes.other = { name: 'other' }; },
    'service uses foreign named volume': (c) => { c.services.postgres.volumes[0].source = 'shared-pgdata'; },
    'bind outside preview roots': (c) => { c.services['cloud-api'].volumes.push({ type: 'bind', source: '/srv/u-dao/data', target: '/data', read_only: true }); },
    'bind host docker socket': (c) => { c.services['cloud-api'].volumes.push({ type: 'bind', source: '/var/run/docker.sock', target: '/var/run/docker.sock' }); },
    'writable repo bind': (c) => { c.services.keycloak.volumes[1].read_only = false; },
    'unknown top-level secret': (c) => { c.secrets.extra_secret = { file: `${PREVIEW.secretRoot}/extra_secret` }; },
    'service extra secret': (c) => { c.services['web-console'].secrets = [{ source: 'app_db_password' }]; },
    'service missing secret': (c) => { c.services['cloud-api'].secrets.pop(); },
  };
  for (const [name, mutate] of Object.entries(cases)) {
    const config = resolvedConfig();
    mutate(config);
    assert.ok(checkResolved(config).length > 0, name + ' must be rejected');
  }
});
