// Static contract check for the B4 Preview Compose configuration.
//
//   node tools/b4-preview-contract.mjs                       # check the source overlay files (no Docker)
//   node tools/b4-preview-contract.mjs --resolved cfg.json   # check `docker compose config --format json` output
//
// The resolved mode is the authoritative pre-deployment check and must run on the
// server (CC) where Docker and the real env file exist. A static PASS is NOT a
// substitute for the server's read-only resource/network/route verification.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import YAML from 'yaml';

export const PREVIEW = {
  project: 'crane-b4-preview',
  origin: 'https://b4.crane.youdaozd.com',
  realm: 'crane-preview',
  client: 'crane-b4-preview',
  webPort: '18083',
  authPort: '18085',
  networks: ['u-dao-crane-b4-preview-frontend', 'u-dao-crane-b4-preview-api-frontend'],
  volumes: ['b4-preview-postgres', 'b4-preview-mqtt'],
  secretRoot: '/srv/u-dao/secrets/crane-b4-preview',
  environment: 'b4-preview',
};

// Production identifiers that must never appear in a Preview configuration.
const FORBIDDEN = ['crane-postgres', 'crane-mqtt', 'u-dao-crane-frontend', '/srv/u-dao/secrets/crane-ops', 'https://crane.youdaozd.com', 'crane-bff'];

function check(errors, condition, message) {
  if (!condition) errors.push(message);
}

/** Checks the source overlay (unresolved ${VAR:-default} values allowed). */
export function checkOverlay(overlay, envExample) {
  const errors = [];
  const services = overlay.services ?? {};
  const api = services['cloud-api']?.environment ?? {};
  check(errors, String(api.OIDC_ISSUER ?? '').endsWith(`/realms/${PREVIEW.realm}`), `cloud-api OIDC_ISSUER must target realm ${PREVIEW.realm}`);
  check(errors, api.OIDC_CLIENT_ID === PREVIEW.client, `cloud-api OIDC_CLIENT_ID must be ${PREVIEW.client}`);
  check(errors, api.CRANE_DEPLOYMENT_ENVIRONMENT === PREVIEW.environment, `cloud-api CRANE_DEPLOYMENT_ENVIRONMENT must be ${PREVIEW.environment}`);
  check(errors, JSON.stringify(services.keycloak?.ports ?? []).includes(PREVIEW.authPort), `keycloak must publish ${PREVIEW.authPort}`);
  check(errors, JSON.stringify(services['web-console']?.ports ?? []).includes(PREVIEW.webPort), `web-console must publish ${PREVIEW.webPort}`);
  check(errors, !services.ingress, 'no second ingress layer: TLS terminates at the existing shared ingress');
  const networks = JSON.stringify(overlay.networks ?? {});
  for (const name of PREVIEW.networks) check(errors, networks.includes(name), `network ${name} must be declared`);
  check(errors, (services['cloud-api']?.networks ?? []).includes('api-frontend'), 'cloud-api must join api-frontend');
  for (const name of PREVIEW.volumes) check(errors, Object.hasOwn(overlay.volumes ?? {}, name), `volume ${name} must be declared`);
  for (const [name, secret] of Object.entries(overlay.secrets ?? {})) {
    check(errors, String(secret.file ?? '').includes('CRANE_SECRET_DIR:?'), `secret ${name} must require CRANE_SECRET_DIR (no production default)`);
  }
  for (const word of FORBIDDEN) check(errors, !JSON.stringify(overlay).includes(word), `overlay must not reference production identifier ${word}`);
  if (envExample !== undefined) {
    // Only assignment lines carry configuration; comments may name production
    // identifiers to explain the separation.
    const assignments = envExample.split('\n').filter((l) => /^[A-Z_]+=/.test(l));
    const env = Object.fromEntries(assignments.map((l) => [l.slice(0, l.indexOf('=')), l.slice(l.indexOf('=') + 1)]));
    check(errors, env.CRANE_PUBLIC_URL === PREVIEW.origin && env.CRANE_AUTH_URL === PREVIEW.origin, 'env example must use the Preview origin');
    check(errors, env.CRANE_WEB_PORT === PREVIEW.webPort && env.CRANE_AUTH_PORT === PREVIEW.authPort, 'env example ports must be 18083/18085');
    check(errors, env.CRANE_SECRET_DIR === PREVIEW.secretRoot, 'env example secret dir must be the Preview root');
    check(errors, /@sha256:/.test(env.CRANE_CLOUD_IMAGE ?? '') && /@sha256:/.test(env.CRANE_WEB_IMAGE ?? ''), 'env example images must be @sha256 digests');
    for (const word of FORBIDDEN) check(errors, !assignments.join('\n').includes(word), `env example must not assign production identifier ${word}`);
  }
  return errors;
}

/** Checks `docker compose config --format json` output (fully resolved). */
export function checkResolved(config) {
  const errors = [];
  check(errors, config.name === PREVIEW.project, `compose project must be ${PREVIEW.project}, got ${config.name}`);
  const services = config.services ?? {};
  for (const [name, service] of Object.entries(services)) {
    const image = String(service.image ?? '');
    check(errors, image !== '' && !/:latest(@|$)/.test(image), `${name}: image must be pinned, not latest`);
    if (name === 'cloud-api' || name === 'web-console') check(errors, /@sha256:[0-9a-f]{64}$/.test(image), `${name}: image must be an @sha256 digest`);
    for (const port of service.ports ?? []) check(errors, String(port.host_ip ?? '') === '127.0.0.1', `${name}: ports must bind 127.0.0.1 only`);
  }
  const api = services['cloud-api']?.environment ?? {};
  check(errors, api.OIDC_ISSUER === `${PREVIEW.origin}/realms/${PREVIEW.realm}`, `issuer must be ${PREVIEW.origin}/realms/${PREVIEW.realm}`);
  check(errors, api.PUBLIC_URL === PREVIEW.origin, `PUBLIC_URL must be ${PREVIEW.origin}`);
  check(errors, api.CRANE_DEPLOYMENT_ENVIRONMENT === PREVIEW.environment, 'environment sentinel must be set');
  const published = Object.values(services).flatMap((s) => (s.ports ?? []).map((p) => String(p.published)));
  check(errors, published.includes(PREVIEW.webPort) && published.includes(PREVIEW.authPort), 'web/auth ports must be published');
  check(errors, new Set(published).size === published.length, 'published ports must not collide');
  const networkNames = Object.values(config.networks ?? {}).map((n) => n.name);
  for (const name of PREVIEW.networks) check(errors, networkNames.includes(name), `network ${name} missing`);
  for (const [key, volume] of Object.entries(config.volumes ?? {})) {
    check(errors, String(volume.name ?? key).startsWith(PREVIEW.project) || PREVIEW.volumes.some((v) => String(volume.name ?? key).endsWith(v)), `volume ${key} must be Preview-scoped`);
  }
  for (const [name, secret] of Object.entries(config.secrets ?? {})) check(errors, String(secret.file ?? '').startsWith(PREVIEW.secretRoot + '/'), `secret ${name} must live under ${PREVIEW.secretRoot}`);
  for (const word of FORBIDDEN) check(errors, !JSON.stringify(config).includes(word), `resolved config must not reference production identifier ${word}`);
  return errors;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  const resolvedIndex = process.argv.indexOf('--resolved');
  const errors = resolvedIndex > 0
    ? checkResolved(JSON.parse(readFileSync(process.argv[resolvedIndex + 1], 'utf8')))
    : checkOverlay(YAML.parse(readFileSync('deploy/compose/docker-compose.b4-preview.yml', 'utf8')), readFileSync('deploy/compose/b4-preview.env.example', 'utf8'));
  for (const error of errors) console.error('B4_PREVIEW_CONTRACT_VIOLATION: ' + error);
  console.log(errors.length === 0 ? `B4_PREVIEW_CONTRACT=PASS mode=${resolvedIndex > 0 ? 'resolved' : 'static-overlay'}` : `B4_PREVIEW_CONTRACT=FAIL violations=${errors.length}`);
  process.exitCode = errors.length === 0 ? 0 : 1;
}
