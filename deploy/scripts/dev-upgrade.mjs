// D1 development release switch for https://crane.youdaozd.com (existing u-dao-crane-ops project).
//
// Modes (all docker calls go through one injectable executor; nothing runs on import):
//   record   <private.env> <out.json>                       read-only: pin the running digests + DB schema
//   plan     <dev-manifest.json> <private.env> <prev.json>   read-only: every precondition, prints the plan
//   apply    <dev-manifest.json> <private.env> <prev.json> [backupDir]
//            separately approved change window only: (backup drill) → pull → up cloud-api web-console
//   rollback <private.env> <prev.json>                      restore the pinned images; STOPs if schema moved
//            (only usable from a checkout of a previous revision that ships this tool; after a
//             migration there is NO image rollback — see recoveryGuidance and the D1 report §4.1)
//
// Only cloud-api and web-console are ever pulled/recreated (--no-deps). PostgreSQL, Keycloak,
// Mosquitto, the shared ingress and other platforms are never touched by this tool.
// Exit codes: 0 PASS · 1 error/refused · 2 STOP condition (human decision required) · 3 smoke FAIL.
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFileSync, statSync, writeFileSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { parseEnv } from 'node:util';
import { productionProject, validateDevelopment, devOrigin as DEV_ORIGIN_, devEnvironment, devOverlay } from '../../tools/production-contract.mjs';
import { verifyRelease } from './release-check.mjs';
import { assertExactCheckout, repositoryRoot } from './source-check.mjs';
import { checkProductionDisks } from './disk-check.mjs';

export const DEV_ORIGIN = DEV_ORIGIN_;
export const DEV_ENVIRONMENT = devEnvironment;
export const UPDATABLE = ['cloud-api', 'web-console'];
export const SHARED = ['postgres', 'keycloak', 'mosquitto'];
export const MAX_BACKUP_AGE_HOURS = 6;
const OVERLAY = devOverlay;
const DIGEST_REF = /^[a-z0-9./-]+(?::[A-Za-z0-9._-]+)?@sha256:[a-f0-9]{64}$/;

export class Stop extends Error { constructor(message) { super('STOP: ' + message); this.code = 2; } }
export { validateDevelopment };

function defaults(options = {}) {
  return {
    root: repositoryRoot, execute: spawnSync, diskGate: checkProductionDisks, now: () => new Date(),
    probe: (url) => {
      const r = spawnSync('curl', ['-sS', '-m', '15', '-o', '-', '-w', '\n%{http_code}', url], { encoding: 'utf8' });
      if (r.error || r.status) return { status: 0, body: '' };
      const i = r.stdout.lastIndexOf('\n');
      return { status: Number(r.stdout.slice(i + 1)), body: r.stdout.slice(0, i) };
    },
    log: (line) => console.log(line), ...options,
  };
}

function loadEnv(ctx, environmentArgument) {
  const envFile = resolve(environmentArgument);
  if (process.platform !== 'win32' && statSync(envFile).mode & 0o077) throw Error('Private env requires mode 0600');
  const environment = parseEnv(readFileSync(envFile, 'utf8'));
  if (environment.CRANE_DISK_PATH || process.env.CRANE_DISK_PATH) throw Error('CRANE_DISK_PATH is retired; DockerRootDir must be discovered');
  for (const key of ['CRANE_CLOUD_IMAGE', 'CRANE_WEB_IMAGE', 'CRANE_DEPLOYMENT_ENVIRONMENT', 'APP_ENV'])
    if (process.env[key] !== undefined) throw Error(`Shell variable ${key} would override the reviewed env file`);
  return { envFile, environment };
}

function docker(ctx, args, capture = true) {
  const result = ctx.execute('docker', args, { cwd: ctx.root, encoding: 'utf8', stdio: capture ? 'pipe' : 'inherit' });
  // Upstream output is never echoed: it may contain resolved configuration.
  if (result.error || result.status !== 0) throw Error('Docker step failed: ' + args.filter(a => !a.startsWith('/')).slice(0, 4).join(' '));
  return result.stdout || '';
}

function compose(ctx, envFile) {
  return ['compose', '--project-name', productionProject, '--project-directory', resolve(ctx.root, 'deploy/compose'), '--env-file', envFile,
    '-f', resolve(ctx.root, 'deploy/compose/docker-compose.prod.yml'), '-f', resolve(ctx.root, OVERLAY)];
}

function resolvedConfig(ctx, envFile) {
  const config = JSON.parse(docker(ctx, [...compose(ctx, envFile), 'config', '--format', 'json']));
  validateDevelopment(config);
  return config;
}

/** Running image reference + source revision of cloud-api and web-console. */
export function running(ctx, envFile) {
  const out = {};
  for (const service of UPDATABLE) {
    const ids = docker(ctx, [...compose(ctx, envFile), 'ps', '-q', service]).trim().split('\n').filter(Boolean);
    if (ids.length !== 1) throw new Stop(`${service} must have exactly one running container (found ${ids.length})`);
    const image = docker(ctx, ['inspect', '--format', '{{.Config.Image}}', ids[0]]).trim();
    const revision = docker(ctx, ['inspect', '--format', '{{index .Config.Labels "org.opencontainers.image.revision"}}', ids[0]]).trim();
    if (!DIGEST_REF.test(image)) throw new Stop(`${service} is not running an immutable image@sha256 reference`);
    if (!/^[a-f0-9]{40}$/.test(revision)) throw new Stop(`${service} image has no full source revision label`);
    out[service] = { image, revision };
  }
  if (out['cloud-api'].revision !== out['web-console'].revision) throw new Stop('cloud-api and web-console run different source revisions');
  return out;
}

export function dbSchema(ctx, envFile) {
  const text = docker(ctx, [...compose(ctx, envFile), 'exec', '-T', '--user', 'postgres', 'postgres', 'psql', '-XqAt', '-v', 'ON_ERROR_STOP=1', '-d', 'crane_app',
    '-c', 'SELECT max(version_id) FROM goose_db_version WHERE is_applied']).trim();
  if (!/^\d+$/.test(text)) throw Error('Cannot read applied schema version');
  return Number(text);
}

function readPrevious(path) {
  const p = JSON.parse(readFileSync(resolve(path), 'utf8'));
  if (p.schema_version !== 1 || p.kind !== 'crane-running-record' || p.project !== productionProject || !/^[a-f0-9]{40}$/.test(p.source_sha || '') ||
    !Number.isInteger(p.db_schema_version) || UPDATABLE.some(s => !DIGEST_REF.test(p.images?.[s] || ''))) throw Error('Invalid previous running record');
  return p;
}

export function record(environmentArgument, out, options) {
  const ctx = defaults(options);
  const { envFile } = loadEnv(ctx, environmentArgument);
  resolvedConfig(ctx, envFile);
  const now = running(ctx, envFile);
  const value = {
    schema_version: 1, kind: 'crane-running-record', project: productionProject, source_sha: now['cloud-api'].revision,
    images: Object.fromEntries(UPDATABLE.map(s => [s, now[s].image])), db_schema_version: dbSchema(ctx, envFile), recorded_at: ctx.now().toISOString(),
  };
  writeFileSync(resolve(out), JSON.stringify(value, null, 2) + '\n', { flag: 'wx', mode: 0o600 });
  ctx.log(`DEV_RECORD=PASS source_sha=${value.source_sha} db_schema=${value.db_schema_version}`);
  return value;
}

export function plan(manifestArgument, environmentArgument, previousArgument, options) {
  const ctx = defaults(options);
  const manifest = verifyRelease(resolve(manifestArgument), ctx.root, 'development');
  const { envFile, environment } = loadEnv(ctx, environmentArgument);
  const previous = readPrevious(previousArgument);
  const disks = ctx.diskGate({ backupRoot: environment.CRANE_BACKUP_ROOT, expectedDockerRoot: environment.CRANE_DOCKER_ROOT_DIR });
  const config = resolvedConfig(ctx, envFile);
  for (const item of manifest.images) {
    const service = item.image_name.endsWith('/cloud-api') ? 'cloud-api' : 'web-console';
    if (config.services[service].image !== item.image_name + '@' + item.image_digest) throw Error(`Env ${service} image does not match the reviewed candidate digest`);
  }
  for (const [service, key] of [['postgres', 'postgres'], ['keycloak', 'keycloak'], ['mosquitto', 'mosquitto']])
    if (config.services[service].image !== manifest.upstream?.[key]) throw new Stop(`${service} image differs from the candidate's pinned upstream; shared services are out of scope`);
  const now = running(ctx, envFile);
  for (const service of UPDATABLE)
    if (now[service].image !== previous.images[service]) throw new Stop(`${service} running image differs from the pinned previous record (re-run record)`);
  const current = dbSchema(ctx, envFile);
  if (current !== previous.db_schema_version) throw new Stop('Database schema changed since the previous record was taken');
  const target = manifest.schema_version_max;
  if (target < current) throw new Stop(`Candidate schema ${target} is older than database schema ${current}`);
  const migration = target > current;
  const result = { source_sha: manifest.source_sha, previous_sha: previous.source_sha, current_schema: current, target_schema: target, migration, services: UPDATABLE, disks };
  ctx.log(`DEV_PLAN=PASS candidate=${manifest.source_sha} previous=${previous.source_sha} schema=${current}->${target} migration=${migration ? 'YES(backup+drill required)' : 'NO'} services=${UPDATABLE.join(',')}`);
  return { ...result, ctx, envFile, manifest, previous };
}

const sha256 = (file) => createHash('sha256').update(readFileSync(file)).digest('hex');

/** A completed backup.py set taken from the previous version at the current schema, recent enough. */
export function verifyBackup(dir, previous, currentSchema, now) {
  const root = resolve(dir);
  if (!/^crane-backup-\d{8}T\d{6}Z-[0-9a-f]{8}$/.test(root.split(/[\\/]/).at(-1))) throw Error('Backup must be a completed crane-backup-* set');
  if (sha256(join(root, 'metadata.json')) !== readFileSync(join(root, 'metadata.sha256'), 'utf8').trim()) throw Error('Backup metadata checksum mismatch');
  const meta = JSON.parse(readFileSync(join(root, 'metadata.json'), 'utf8'));
  for (const [label, item] of Object.entries(meta.databases || {}))
    if (item.file !== label + '.dump' || sha256(join(root, item.file)) !== item.sha256) throw Error('Backup dump checksum mismatch');
  if (Object.keys(meta.databases || {}).sort().join() !== 'application,keycloak') throw Error('Backup must contain application and keycloak');
  if (meta.source_sha !== previous.source_sha) throw Error('Backup was not taken from the previous running version');
  if (meta.databases.application.snapshot?.schema_version !== currentSchema) throw Error('Backup schema differs from the current database schema');
  const age = (now.getTime() - Date.parse(meta.timestamp)) / 3600e3;
  if (!(age >= 0 && age <= MAX_BACKUP_AGE_HOURS)) throw Error(`Backup must be at most ${MAX_BACKUP_AGE_HOURS}h old`);
  return true;
}

function smoke(ctx) {
  const checks = [];
  const check = (name, ok) => { checks.push(`${name}=${ok ? 'PASS' : 'FAIL'}`); return ok; };
  const home = ctx.probe(DEV_ORIGIN + '/');
  check('web', home.status === 200);
  const discovery = ctx.probe(DEV_ORIGIN + '/realms/crane/.well-known/openid-configuration');
  let issuer = '';
  try { issuer = JSON.parse(discovery.body).issuer; } catch { /* reported below */ }
  check('oidc_issuer', discovery.status === 200 && issuer === DEV_ORIGIN + '/realms/crane');
  check('api_requires_login', ctx.probe(DEV_ORIGIN + '/api/v1/projects').status === 401);
  check('health_not_public', [403, 404].includes(ctx.probe(DEV_ORIGIN + '/health').status));
  check('admin_not_public', [403, 404].includes(ctx.probe(DEV_ORIGIN + '/admin/').status));
  return { ok: checks.every(c => c.endsWith('PASS')), checks };
}

export function apply(manifestArgument, environmentArgument, previousArgument, backupArgument, options) {
  const p = plan(manifestArgument, environmentArgument, previousArgument, options);
  const { ctx, envFile, manifest, previous } = p;
  assertExactCheckout(manifest.source_sha, ctx.root);
  if (p.migration) {
    if (!backupArgument) throw new Stop(`Migration ${p.current_schema}->${p.target_schema} requires a fresh backup set argument`);
    verifyBackup(backupArgument, previous, p.current_schema, ctx.now());
    const drill = ctx.execute('python3', [resolve(ctx.root, 'deploy/scripts/backup.py'), 'drill', '--backup', resolve(backupArgument), '--env-file', envFile,
      '--compose', resolve(ctx.root, 'deploy/compose/docker-compose.prod.yml')], { cwd: ctx.root, encoding: 'utf8', stdio: 'pipe' });
    if (drill.error || drill.status !== 0) throw new Stop('Isolated restore drill failed; upgrade not started');
    ctx.log('DEV_BACKUP_DRILL=PASS');
  }
  docker(ctx, [...compose(ctx, envFile), 'pull', ...UPDATABLE], false);
  assertExactCheckout(manifest.source_sha, ctx.root); // pull can take minutes: recheck before up
  docker(ctx, [...compose(ctx, envFile), 'up', '-d', '--no-build', '--no-deps', '--wait', ...UPDATABLE], false);
  const now = running(ctx, envFile);
  for (const item of manifest.images) {
    const service = item.image_name.endsWith('/cloud-api') ? 'cloud-api' : 'web-console';
    if (now[service].image !== item.image_name + '@' + item.image_digest || now[service].revision !== manifest.source_sha) throw Error(`${service} is not running the candidate after up`);
  }
  if (dbSchema(ctx, envFile) !== p.target_schema) throw Error('Applied schema differs from the candidate schema');
  const s = smoke(ctx);
  ctx.log('DEV_SMOKE ' + s.checks.join(' '));
  if (!s.ok) {
    const e = Error('Smoke failed. Do not continue. ' + recoveryGuidance(ctx.root, previous, p));
    e.code = 3; throw e;
  }
  ctx.log(`DEV_APPLY=PASS source_sha=${manifest.source_sha} schema=${p.target_schema} — browser login + business smoke still required (manual checklist)`);
  return p;
}

/** True when the previous source revision itself ships this tool and the crane-dev overlay. */
export function previousHasTool(root, sha) {
  if (!/^[a-f0-9]{40}$/.test(sha || '')) return false;
  for (const path of ['deploy/scripts/dev-upgrade.mjs', devOverlay]) {
    const r = spawnSync('git', ['-C', root, 'cat-file', '-e', `${sha}:${path}`], { stdio: 'ignore' });
    if (r.error || r.status !== 0) return false;
  }
  return true;
}

/**
 * Recovery guidance after a failed upgrade. Only states what is executable:
 *  - schema moved (migration): NO image rollback at all — the old binary is not proven against
 *    the new schema; recovery is a verified restore from the pre-upgrade backup, decided by OWNER;
 *  - previous revision without this tool (e.g. the first upgrade from a pre-D1 version): there is
 *    no automatic rollback — `git checkout <old> && dev-upgrade rollback` cannot run there;
 *  - otherwise: the rollback command of the previous revision's own checkout.
 */
export function recoveryGuidance(root, previous, plan) {
  if (plan.migration)
    return `STOP: schema moved ${plan.current_schema}->${plan.target_schema}. Do NOT roll back images (old-binary compatibility with the new schema is not proven). ` +
      'Recover only by the verified manual restore procedure (pre-upgrade backup set + isolated drill already passed), with OWNER approval; see docs/08-部署运维与成本/D1-单域名开发发布实施与验收报告.md §4.1.';
  if (!previousHasTool(root, previous.source_sha))
    return `STOP: the previous revision ${previous.source_sha} does not contain dev-upgrade.mjs / the crane-dev overlay, so no automatic rollback exists for it. ` +
      'Use the verified manual procedure (restore the previous env file, recreate only cloud-api and web-console from the pinned previous digests from the retained control checkout) with OWNER approval; see §4.1.';
  return `Rollback: in a clean checkout of ${previous.source_sha}, restore the previous env file, then run node deploy/scripts/dev-upgrade.mjs rollback <env> <prev.json>.`;
}

export function rollback(environmentArgument, previousArgument, options) {
  const ctx = defaults(options);
  const previous = readPrevious(previousArgument);
  assertExactCheckout(previous.source_sha, ctx.root);
  const { envFile } = loadEnv(ctx, environmentArgument);
  const config = resolvedConfig(ctx, envFile);
  for (const service of UPDATABLE)
    if (config.services[service].image !== previous.images[service]) throw Error(`Env ${service} image is not the pinned previous digest; restore the previous env file first`);
  const current = dbSchema(ctx, envFile);
  if (current !== previous.db_schema_version)
    throw new Stop(`Database schema is ${current}, previous version ran on ${previous.db_schema_version}. Old-binary compatibility is not proven; image rollback refused. Decide restore-from-backup separately (OWNER approval).`);
  docker(ctx, [...compose(ctx, envFile), 'up', '-d', '--no-build', '--no-deps', '--wait', ...UPDATABLE], false);
  const now = running(ctx, envFile);
  for (const service of UPDATABLE) if (now[service].image !== previous.images[service]) throw Error(`${service} did not return to the previous image`);
  const s = smoke(ctx);
  ctx.log('DEV_SMOKE ' + s.checks.join(' '));
  if (!s.ok) { const e = Error('Rollback completed but smoke failed; escalate'); e.code = 3; throw e; }
  ctx.log(`DEV_ROLLBACK=PASS source_sha=${previous.source_sha}`);
}

if (import.meta.main) {
  const [mode, ...args] = process.argv.slice(2);
  try {
    if (mode === 'record' && args.length === 2) record(...args);
    else if (mode === 'plan' && args.length === 3) plan(...args);
    else if (mode === 'apply' && (args.length === 3 || args.length === 4)) apply(args[0], args[1], args[2], args[3]);
    else if (mode === 'rollback' && args.length === 2) rollback(...args);
    else throw Error('Usage: dev-upgrade.mjs record <env> <out.json> | plan <manifest> <env> <prev.json> | apply <manifest> <env> <prev.json> [backupDir] | rollback <env> <prev.json>');
  } catch (error) { console.error(error.message); process.exitCode = error.code || 1; }
}
