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
});

const digest = (c) => `ghcr.io/x/${c}@sha256:${'a'.repeat(64)}`;
function resolvedConfig() {
  const port = (published) => ({ host_ip: '127.0.0.1', published, target: 8080 });
  return {
    name: PREVIEW.project,
    services: {
      'cloud-api': { image: digest('cloud-api'), environment: { OIDC_ISSUER: `${PREVIEW.origin}/realms/${PREVIEW.realm}`, PUBLIC_URL: PREVIEW.origin, CRANE_DEPLOYMENT_ENVIRONMENT: PREVIEW.environment } },
      'web-console': { image: digest('web-console'), ports: [port(PREVIEW.webPort)] },
      keycloak: { image: 'quay.io/keycloak/keycloak:26.8.1@sha256:' + 'b'.repeat(64), ports: [port(PREVIEW.authPort)] },
    },
    networks: { frontend: { name: PREVIEW.networks[0] }, 'api-frontend': { name: PREVIEW.networks[1] }, backend: { name: 'crane-b4-preview_backend' } },
    volumes: { 'b4-preview-postgres': { name: 'crane-b4-preview_b4-preview-postgres' } },
    secrets: { app_db_password: { file: `${PREVIEW.secretRoot}/app_db_password` } },
  };
}

test('B4 Preview resolved 配置通过契约', () => {
  assert.deepEqual(checkResolved(resolvedConfig()), []);
});

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
  };
  for (const [name, mutate] of Object.entries(cases)) {
    const config = resolvedConfig();
    mutate(config);
    assert.ok(checkResolved(config).length > 0, name + ' must be rejected');
  }
});
