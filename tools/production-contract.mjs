import { readFileSync } from 'node:fs';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { frontendNetwork, validateIngress } from './production-topology.mjs';

export const productionProject = 'u-dao-crane-ops';
export function validateProduction(config) {
  assert.equal(config.name, productionProject, 'Production Compose project is frozen');
  assert.deepEqual(Object.keys(config.services).sort(), ['cloud-api', 'keycloak', 'mosquitto', 'postgres', 'web-console']);
  for (const [name, service] of Object.entries(config.services)) {
    assert.match(service.image, /@sha256:[a-f0-9]{64}$/, `${name} must use an immutable image`);
    assert.equal(service.logging.driver, 'json-file');
    assert.equal(service.logging.options['max-size'], '10m');
    assert.equal(service.logging.options['max-file'], '5');
    for (const port of service.ports || []) {
      assert.equal(port.host_ip, '127.0.0.1');
      assert.ok(!['80', '443'].includes(String(port.published)));
    }
  }
  assert.equal(config.services.postgres.ports, undefined);
  assert.equal(config.services.mosquitto.ports, undefined);
  assert.equal(config.services['cloud-api'].ports, undefined);
  assert.equal(config.networks.frontend.external, true, 'Frontend must be externally owned');
  assert.ok(config.networks.frontend.name, 'Explicit external frontend network name required');
  assert.equal(config.networks.backend.internal, true, 'Backend must remain isolated');
  for (const name of ['web-console', 'keycloak']) assert.ok(Object.hasOwn(config.services[name].networks, 'frontend'), `${name} must join frontend`);
  assert.equal(config.services.postgres.networks.frontend, undefined);
  assert.equal(config.services.mosquitto.networks.frontend, undefined);
  assert.equal(config.services['cloud-api'].environment.APP_ENV, 'production');
  assert.equal(config.services['cloud-api'].environment.DEV_API_TOKEN, undefined);
  assert.equal(config.services['cloud-api'].environment.OIDC_CLIENT_SECRET_FILE, '/run/secrets/oidc_client_secret');
  assert.equal(config.services.keycloak.environment.KC_HOSTNAME_STRICT, 'true');
  assert.equal(config.services.keycloak.environment.KC_DB, 'postgres');
  assert.equal(config.services['cloud-api'].environment.OIDC_ISSUER, config.services.keycloak.environment.KC_HOSTNAME + '/realms/crane');
  const theme = config.services.keycloak.volumes.find(volume => volume.target === '/opt/keycloak/themes/u-dao');
  assert.equal(theme?.read_only, true, 'Keycloak theme must be versioned and mounted read-only');
  assert.ok(!JSON.stringify(config.services.keycloak).includes('start-dev'));
  assert.equal(Object.keys(config.secrets).length, 6);
  for (const item of Object.values(config.secrets)) assert.equal(typeof item.file, 'string');
  return true;
}

export async function productionContract(docker = 'docker') {
  const { default: YAML } = await import('yaml');
  const fakeDigest = 'a'.repeat(64);
  const env = {
    ...process.env, CRANE_CLOUD_IMAGE: `example.invalid/crane-cloud@sha256:${fakeDigest}`, CRANE_WEB_IMAGE: `example.invalid/crane-web@sha256:${fakeDigest}`,
    CRANE_PROXY_CIDRS: '172.30.15.0/28', CRANE_SECRET_DIR: '/anonymous/secrets', CRANE_MQTT_DIR: '/anonymous/mqtt', CRANE_MQTT_ACL: '/anonymous/acl',
    CRANE_INVENTORY_FILE: '/anonymous/inventory.json', CRANE_FLEET_FILE: '/anonymous/fleet.json',
    CRANE_FRONTEND_NETWORK: frontendNetwork,
  };
  const result = spawnSync(docker, ['compose', '-f', 'deploy/compose/docker-compose.prod.yml', 'config', '--format', 'json'], { env, encoding: 'utf8' });
  if (result.error || result.status) throw Error('Production Compose resolution failed');
  const config = JSON.parse(result.stdout);
  validateProduction(config);
  assert.equal(config.networks.frontend.name, frontendNetwork);
  validateIngress();
  console.log('PRODUCTION_INGRESS_NO_LOOPBACK_UPSTREAM=PASS');
  console.log('PRODUCTION_INGRESS_DOCKER_DNS=PASS');
  console.log('PRODUCTION_FRONTEND_EXTERNAL_NETWORK=PASS');
  const realm = JSON.parse(readFileSync('deploy/production/realm.template.json', 'utf8'));
  assert.equal(realm.adminEventsDetailsEnabled, false);
  assert.equal(realm.loginTheme, 'u-dao');
  assert.equal(realm.requiredActions.find(action => action.alias === 'UPDATE_PASSWORD')?.enabled, true);
  assert.match(readFileSync('deploy/keycloak/themes/u-dao/login/theme.properties', 'utf8'), /parent=keycloak/);
  const client = realm.clients[0];
  assert.equal(client.publicClient, false); assert.equal(client.directAccessGrantsEnabled, false); assert.equal(client.implicitFlowEnabled, false);
  assert.equal(client.attributes['pkce.code.challenge.method'], 'S256');
  assert.deepEqual(client.redirectUris, ['${CRANE_PUBLIC_URL}/auth/callback']);
  assert.equal(client.secret, undefined); assert.equal(realm.users, undefined);
  const simulation = YAML.parse(readFileSync('deploy/compose/docker-compose.prod.simulation.yml', 'utf8'));
  assert.deepEqual(simulation.services['device-simulator'].profiles, ['simulation']);
  for (const file of ['.gitignore', '.dockerignore']) {
    const text = readFileSync(file, 'utf8');
    for (const excluded of ['local-private', 'deploy/compose/runtime', 'deploy/compose/production.env']) assert.ok(text.includes(excluded));
  }
  console.log('PRODUCTION_COMPOSE_CONTRACT=PASS default_services=5 simulator=OFF remote=OFF pointcloud=OFF');
}
if (process.argv[1]?.endsWith('production-contract.mjs')) {
  try { await productionContract(process.platform === 'win32' ? 'C:/Program Files/Docker/Docker/resources/bin/docker.exe' : 'docker'); }
  catch (error) { console.error(error.message); process.exitCode = 1; }
}
