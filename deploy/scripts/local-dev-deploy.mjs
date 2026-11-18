// Source-direct deployment for the crane-dev environment ONLY (https://crane.youdaozd.com,
// development stage). OWNER decision 2026-11-17: no registry images, no GHCR candidate, no dev
// tags, no OCI transport — the server builds the committed source locally with the reviewed
// Dockerfiles and runs those exact local images. The production release path (release.mjs,
// release-check.mjs, upgrade.mjs, dev-upgrade.mjs) is untouched and keeps its digest checks.
//
//   node deploy/scripts/local-dev-deploy.mjs build    <private.env> [--services cloud-api,web-console,device-simulator] [--goproxy https://goproxy.cn,direct]
//   node deploy/scripts/local-dev-deploy.mjs plan     <private.env>
//   node deploy/scripts/local-dev-deploy.mjs apply    <private.env> [--backup <fresh crane-backup-* set>]
//   node deploy/scripts/local-dev-deploy.mjs rollback <private.env>
//   node deploy/scripts/local-dev-deploy.mjs status   <private.env>
//   node deploy/scripts/local-dev-deploy.mjs compose  <private.env> -- <docker compose args…>   (maintenance with the ACTIVE override)
//   node deploy/scripts/local-dev-deploy.mjs fleet    <private.env> --fleet <new fleet.json> --acl <new acl>
//   node deploy/scripts/local-dev-deploy.mjs reset-business <private.env> plan
//   node deploy/scripts/local-dev-deploy.mjs reset-business <private.env> apply --approve <digest> --actor <admin> --confirm crane-dev
//            ONE-OFF crane-dev business reset (OWNER-approved): database business data (one transaction,
//            guarded, audited), Inventory/Fleet/ACL to explicit empty documents, simulator stopped and
//            its spool volume removed, broker ACL reloaded, cloud-api recreated; keeps Keycloak, CA,
//            OIDC, accounts, roles, tenants, portfolio scopes and the equipment type directory
//
// Guarantees:
//   - the checkout must be a clean worktree at a full 40-hex commit; the image tag is that SHA
//     (crane-local/<service>:<sha>), built with SOURCE_SHA=<sha> and verified via its revision label;
//   - an existing crane-local/<service>:<sha> is NEVER overwritten: a rebuild must produce the same
//     content or the build is refused (no "same SHA, different bytes");
//   - only services whose build inputs changed since the running version are rebuilt (git diff of
//     each service's input paths); unchanged services keep their running image;
//   - apply runs `up --no-build --pull never --no-deps` for cloud-api / web-console only, then checks
//     the RUNNING container image ID equals the built image ID and its revision equals the SHA;
//   - the compose override that swaps in local images is generated per deploy, contains only
//     `image:` + `pull_policy: never` for the two services, and refuses any other environment than
//     crane-dev (APP_ENV=production, OIDC, CSRF, TLS, RBAC stay exactly as in production);
//   - state lives under CRANE_LOCAL_DEPLOY_DIR (default /srv/u-dao/releases/crane-local), mode 0600:
//       current.json   the last CONFIRMED deployment (running images + schema + smoke all passed);
//                      before the first local apply it is the captured baseline of what was running
//                      (registry R6 image IDs/refs, compose working dir, env digest, schema), so a
//                      failed first apply rolls back to the local R6 images without any registry;
//       previous.json  the confirmed deployment before current;
//       attempt.json   the last apply attempt (IN_PROGRESS / FAILED / CONFIRMED + reason);
//       active-override.yml  the override of the confirmed deployment; every later compose operation
//                      (restart, Fleet refresh, simulator) must include it (`compose` mode does);
//     current.json is written only after the running image IDs, the schema and the smoke passed;
//   - one deploy at a time: a lock directory (deploy.lock) guards build/apply/rollback/fleet;
//   - build takes an explicit, validated GOPROXY (--goproxy or CRANE_BUILD_GOPROXY) as a build
//     argument only; go.sum and the Go checksum database stay in force (GOSUMDB/GOFLAGS untouched),
//     no global Docker or system proxy is changed;
//   - nothing is pushed; postgres / keycloak / other projects are never touched; mosquitto only
//     receives SIGHUP in `fleet` mode (ACL reload, no restart).
// Exit: 0 ok · 1 refused/error · 2 STOP (human decision) · 3 smoke failed.
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync, chmodSync, rmSync, renameSync, statSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { hostname } from 'node:os';
import { join, resolve } from 'node:path';
import { assertExactCheckout } from './source-check.mjs';
import { defaults, loadEnv, docker, compose, smoke, dbSchema, Stop, verifyBackup } from './dev-upgrade.mjs';
import { productionProject, validateProduction, devEnvironment, devOrigin, devOverlay } from '../../tools/production-contract.mjs';

export const LOCAL_REPO = 'crane-local';
export const DEPLOYABLE = ['cloud-api', 'web-console'];
export const BUILDABLE = {
  'cloud-api': { dockerfile: 'deploy/Dockerfile.backend', go: true, args: [], inputs: ['go.mod', 'go.sum', 'contracts', 'internal', 'services', 'edge', 'tools/identity', 'tools/equipment-types', 'tools/runtime-identity', 'tools/provision-keycloak', 'deploy/postgres', 'deploy/Dockerfile.backend', 'licenses', 'THIRD_PARTY_NOTICES.md'] },
  'web-console': { dockerfile: 'deploy/Dockerfile.web-production', go: false, args: [], inputs: ['package.json', 'package-lock.json', 'apps/web-console', 'contracts', 'deploy/nginx/production.conf', 'deploy/Dockerfile.web-production', 'licenses', 'THIRD_PARTY_NOTICES.md'] },
  'device-simulator': { dockerfile: 'deploy/Dockerfile.backend', go: true, args: ['TARGET=simulators/device-simulator'], inputs: ['go.mod', 'go.sum', 'contracts', 'internal', 'edge', 'simulators', 'deploy/Dockerfile.backend'] },
};
const SHA = /^[a-f0-9]{40}$/;
const IMAGE_ID = /^sha256:[a-f0-9]{64}$/;
export const localTag = (service, sha) => `${LOCAL_REPO}/${service}:${sha}`;

function git(ctx, ...args) {
  const r = ctx.execute('git', ['-C', ctx.root, ...args], { encoding: 'utf8' });
  if (r.error || r.status !== 0) throw Error('git ' + args[0] + ' failed');
  return (r.stdout || '').trim();
}

export function stateDir(environment) {
  return resolve(environment.CRANE_LOCAL_DEPLOY_DIR || '/srv/u-dao/releases/crane-local');
}

function readState(dir, name) {
  const p = join(dir, name);
  return existsSync(p) ? JSON.parse(readFileSync(p, 'utf8')) : null;
}
function writeState(dir, name, value) {
  mkdirSync(dir, { recursive: true, mode: 0o700 });
  chmodSync(dir, 0o700);
  const tmp = join(dir, `.${name}.${process.pid}`);
  writeFileSync(tmp, typeof value === 'string' ? value : JSON.stringify(value, null, 2) + '\n', { mode: 0o600 });
  // rename is atomic within the directory: a crash never leaves a half-written record.
  renameSync(tmp, join(dir, name));
}
const sha256 = (b) => createHash('sha256').update(b).digest('hex');

/** Single-instance deployment lock (mkdir is atomic). A stale lock is reported, never stolen. */
export function withLock(ctx, dir, operation, fn) {
  mkdirSync(dir, { recursive: true, mode: 0o700 });
  const lock = join(dir, 'deploy.lock');
  try { mkdirSync(lock, { mode: 0o700 }); } catch {
    let holder = '';
    try { holder = readFileSync(join(lock, 'owner.json'), 'utf8').trim(); } catch { /* being created */ }
    throw new Stop(`another local deployment operation holds ${lock} ${holder} — wait for it; remove the directory only after confirming that process is gone`);
  }
  try {
    writeFileSync(join(lock, 'owner.json'), JSON.stringify({ operation, pid: process.pid, host: hostname(), since: ctx.now().toISOString() }) + '\n', { mode: 0o600 });
    return fn();
  } finally { rmSync(lock, { recursive: true, force: true }); }
}

const GOPROXY = /^(https:\/\/[a-z0-9.-]+(:\d+)?(\/[A-Za-z0-9._~/-]*)?|direct|off)(,(https:\/\/[a-z0-9.-]+(:\d+)?(\/[A-Za-z0-9._~/-]*)?|direct|off))*$/;
/** Explicit build-time Go module proxy: https URLs / direct / off only; never credentials. */
export function buildGoproxy(environment, flag) {
  const value = flag ?? environment.CRANE_BUILD_GOPROXY ?? '';
  if (value === '') return null;
  if (!GOPROXY.test(value) || value.includes('@')) throw Error('GOPROXY must be a comma list of https:// URLs, direct or off (no credentials)');
  return value;
}

/** Image metadata of a local tag, or null if absent. */
export function inspectImage(ctx, ref) {
  const r = ctx.execute('docker', ['image', 'inspect', '--format', '{{json .}}', ref], { encoding: 'utf8' });
  if (r.error || r.status !== 0) return null;
  const v = JSON.parse(r.stdout);
  return { id: v.Id, revision: v.Config?.Labels?.['org.opencontainers.image.revision'] ?? '', os: v.Os, architecture: v.Architecture, repoDigests: v.RepoDigests ?? [] };
}

/** Services whose build inputs differ between `from` and `to` (all of them when from is unknown). */
export function changedServices(ctx, from, to, services) {
  if (!from || !SHA.test(from)) return services;
  return services.filter((s) => git(ctx, 'diff', '--name-only', from, to, '--', ...BUILDABLE[s].inputs) !== '');
}

/**
 * Build (or confirm) crane-local/<service>:<sha> for each service. Never overwrites an existing tag:
 * if it exists, it must carry the same revision; a fresh build of the same SHA must yield the same
 * image ID (deterministic content) or the build is refused.
 */
export function build(environmentArgument, services = Object.keys(BUILDABLE), options, goproxyFlag) {
  const ctx = defaults(options);
  const { environment } = loadEnv(ctx, environmentArgument);
  const goproxy = buildGoproxy(environment, goproxyFlag);
  return withLock(ctx, stateDir(environment), 'build', () => buildLocked(ctx, environment, services, goproxy));
}
function buildLocked(ctx, environment, services, goproxy) {
  const sha = git(ctx, 'rev-parse', 'HEAD');
  assertExactCheckout(sha, ctx.root);
  const out = {};
  for (const service of services) {
    const spec = BUILDABLE[service];
    if (!spec) throw Error('Unknown service ' + service);
    const tag = localTag(service, sha);
    const existing = inspectImage(ctx, tag);
    const scratch = `${LOCAL_REPO}/${service}:build-${sha.slice(0, 12)}-${process.pid}`;
    // GOPROXY only reaches the Go build stage (go mod download verifies go.sum + checksum DB).
    const proxyArgs = goproxy && spec.go ? ['--build-arg', `GOPROXY=${goproxy}`] : [];
    docker(ctx, ['build', '--pull=false', '--build-arg', `SOURCE_SHA=${sha}`, ...proxyArgs, ...spec.args.flatMap((a) => ['--build-arg', a]), '-f', spec.dockerfile, '-t', scratch, '.'], false);
    const built = inspectImage(ctx, scratch);
    if (!built || !IMAGE_ID.test(built.id) || built.revision !== sha) throw Error(`${service}: built image revision is not ${sha}`);
    if (existing) {
      if (existing.revision !== sha) throw new Stop(`${tag} exists with revision ${existing.revision}; refusing to overwrite`);
      if (existing.id !== built.id) {
        docker(ctx, ['image', 'rm', scratch]);
        throw new Stop(`${tag} exists with different content (${existing.id} ≠ ${built.id}); the same SHA must not name two images`);
      }
      docker(ctx, ['image', 'rm', scratch]);
      out[service] = { tag, id: existing.id, platform: `${existing.os}/${existing.architecture}`, reused: true };
    } else {
      docker(ctx, ['tag', scratch, tag]);
      docker(ctx, ['image', 'rm', scratch]);
      out[service] = { tag, id: built.id, platform: `${built.os}/${built.architecture}`, reused: false };
    }
    ctx.log(`LOCAL_BUILD ${service} ${tag} ${out[service].id} ${out[service].platform}${out[service].reused ? ' (existing, identical)' : ''}`);
  }
  const dir = stateDir(environment);
  writeState(dir, `build-${sha}.json`, { schema_version: 1, kind: 'crane-local-build', source_sha: sha, built_at: ctx.now().toISOString(), goproxy: goproxy ?? 'Dockerfile default', images: out });
  ctx.log(`LOCAL_DEV_BUILD=PASS source_sha=${sha} services=${services.join(',')} goproxy=${goproxy ?? 'default'} pushed=NONE`);
  return { sha, images: out };
}

/** The compose override that swaps in the local images. Generated, never hand-edited. */
export function overrideYaml(images) {
  let y = '# Generated by deploy/scripts/local-dev-deploy.mjs for crane-dev ONLY. Local images, never pulled.\nservices:\n';
  for (const s of DEPLOYABLE) y += `  ${s}:\n    image: ${images[s]}\n    pull_policy: never\n`;
  return y;
}

/** Same checks as production except the two app images are local crane-local/<svc>:<sha> tags. */
export function validateLocalDevelopment(config, sha) {
  const copy = structuredClone(config);
  for (const s of DEPLOYABLE) {
    const want = localTag(s, sha);
    if (copy.services[s].image !== want) throw Error(`${s} must run ${want}`);
    if (copy.services[s].pull_policy !== 'never') throw Error(`${s} must never be pulled`);
    copy.services[s].image = `local.invalid/${s}@sha256:${'0'.repeat(64)}`;
    delete copy.services[s].pull_policy;
  }
  validateProduction(copy);
  const api = config.services['cloud-api'].environment;
  if (api.APP_ENV !== 'production' || api.CRANE_DEPLOYMENT_ENVIRONMENT !== devEnvironment || api.PUBLIC_URL !== devOrigin) throw Error('local images are allowed only for the crane-dev environment with APP_ENV=production');
  return true;
}

function composeArgs(ctx, envFile, overridePath) {
  return ['compose', '--project-name', productionProject, '--project-directory', resolve(ctx.root, 'deploy/compose'), '--env-file', envFile,
    '-f', resolve(ctx.root, 'deploy/compose/docker-compose.prod.yml'), '-f', resolve(ctx.root, devOverlay), '-f', overridePath];
}

function runningContainer(ctx, envFile, overridePath, service) {
  const ids = docker(ctx, [...composeArgs(ctx, envFile, overridePath), 'ps', '-q', service]).trim().split('\n').filter(Boolean);
  if (ids.length !== 1) throw new Stop(`${service} must have exactly one running container (found ${ids.length})`);
  const image = docker(ctx, ['inspect', '--format', '{{.Image}}', ids[0]]).trim();
  const ref = docker(ctx, ['inspect', '--format', '{{.Config.Image}}', ids[0]]).trim();
  const revision = docker(ctx, ['inspect', '--format', '{{index .Config.Labels "org.opencontainers.image.revision"}}', ids[0]]).trim();
  return { id: image, ref, revision };
}

/**
 * Baseline of what runs before the FIRST local deployment (the registry R6 release): image IDs,
 * registry refs, revisions, the compose working directory and config files the containers were
 * created from, the env-file digest, the resolved-config digest and the schema. Each running image
 * is pinned by a local tag crane-local/<svc>:baseline-<revision> (same image ID, nothing pulled),
 * so a failed first apply — or a later rollback — returns to it without any registry.
 */
export function captureBaseline(ctx, envFile, dir) {
  const devArgs = compose(ctx, envFile);
  const images = {};
  let working = null;
  for (const s of DEPLOYABLE) {
    const ids = docker(ctx, [...devArgs, 'ps', '-q', s]).trim().split('\n').filter(Boolean);
    if (ids.length !== 1) throw new Stop(`baseline: ${s} must have exactly one running container (found ${ids.length})`);
    const v = JSON.parse(docker(ctx, ['inspect', '--format', '{{json .}}', ids[0]]));
    const id = v.Image;
    if (!IMAGE_ID.test(id)) throw new Stop(`baseline: ${s} image ID unreadable`);
    const labels = v.Config?.Labels ?? {};
    const revision = labels['org.opencontainers.image.revision'] ?? '';
    const tag = `${LOCAL_REPO}/${s}:baseline-${SHA.test(revision) ? revision : id.slice(7, 19)}`;
    const existing = inspectImage(ctx, tag);
    if (existing && existing.id !== id) throw new Stop(`${tag} already names another image (${existing.id}); refusing to re-pin`);
    if (!existing) docker(ctx, ['tag', id, tag]);
    images[s] = { tag, id, ref: v.Config?.Image ?? '', revision, platform: `${v.Os ?? 'linux'}/${v.Architecture ?? ''}`.replace(/\/$/, '') };
    working ??= { dir: labels['com.docker.compose.project.working_dir'] ?? '', files: labels['com.docker.compose.project.config_files'] ?? '' };
  }
  const resolved = docker(ctx, [...devArgs, 'config', '--format', 'json']);
  const record = { schema_version: 1, kind: 'crane-baseline', project: productionProject, environment: devEnvironment,
    source_sha: SHA.test(images['cloud-api'].revision) ? images['cloud-api'].revision : '', captured_at: ctx.now().toISOString(), images,
    compose_working_dir: working.dir, compose_config_files: working.files, env_file: envFile, env_sha256: sha256(readFileSync(envFile)), resolved_config_sha256: sha256(resolved),
    db_schema_version: dbSchema(ctx, envFile), rollback: 'node deploy/scripts/local-dev-deploy.mjs rollback <private.env>  (local baseline tags, no registry)' };
  writeState(dir, 'baseline.json', record);
  ctx.log(`LOCAL_DEV_BASELINE=CAPTURED ` + DEPLOYABLE.map((s) => `${s}=${images[s].ref}→${images[s].tag} ${images[s].id}`).join(' ') + ` schema=${record.db_schema_version}`);
  return record;
}

/** The override that makes compose run exactly the images of a record (local tags, never pulled). */
function writeOverride(dir, name, record) {
  const path = join(dir, name);
  writeState(dir, name, overrideYaml(Object.fromEntries(DEPLOYABLE.map((s) => [s, record.images[s].tag]))));
  return path;
}
export const ACTIVE_OVERRIDE = 'active-override.yml';

/** Read-only (apart from writing the candidate override): what apply would do. */
export function plan(environmentArgument, options) {
  const ctx = defaults(options);
  const { envFile, environment } = loadEnv(ctx, environmentArgument);
  const sha = git(ctx, 'rev-parse', 'HEAD');
  assertExactCheckout(sha, ctx.root);
  const dir = stateDir(environment);
  const current = readState(dir, 'current.json');
  const images = {};
  for (const s of DEPLOYABLE) {
    // Only a confirmed LOCAL deployment can carry an unchanged service; the first local deploy
    // builds both services from source (the baseline registry images are only the fallback).
    const unchanged = current?.kind === 'crane-local-deploy' && current.source_sha !== sha && !changedServices(ctx, current.source_sha, sha, [s]).length && current.images?.[s];
    const tag = unchanged ? current.images[s].tag : localTag(s, sha);
    const meta = inspectImage(ctx, tag);
    if (!meta) throw new Stop(`${tag} is not built yet: run build first`);
    images[s] = { tag, id: meta.id, revision: meta.revision, platform: `${meta.os}/${meta.architecture}`, carried: !!unchanged };
  }
  mkdirSync(dir, { recursive: true, mode: 0o700 });
  const overridePath = writeOverride(dir, `override-${sha}.yml`, { images });
  const config = JSON.parse(docker(ctx, [...composeArgs(ctx, envFile, overridePath), 'config', '--format', 'json']));
  // validateLocalDevelopment expects <svc>:<sha> for changed services; carried ones keep their tag.
  const forCheck = structuredClone(config);
  for (const s of DEPLOYABLE) if (images[s].carried) forCheck.services[s].image = localTag(s, sha);
  validateLocalDevelopment(forCheck, sha);
  const schema = dbSchema(ctx, envFile);
  const migrations = readdirSync(resolve(ctx.root, 'deploy/postgres/migrations')).map((f) => /^(\d{5})_/.exec(f)?.[1]).filter(Boolean).map(Number);
  const target = Math.max(...migrations);
  if (target < schema) throw new Stop(`source schema ${target} is older than database schema ${schema}`);
  ctx.log(`LOCAL_DEV_PLAN=PASS source_sha=${sha} current=${current ? `${current.kind}:${current.source_sha || 'unknown'}` : 'none (baseline will be captured)'} schema=${schema}->${target} migration=${target > schema ? 'YES(backup required)' : 'NO'} ` +
    DEPLOYABLE.map((s) => `${s}=${images[s].tag}${images[s].carried ? '(unchanged)' : ''}`).join(' '));
  return { ctx, envFile, environment, dir, sha, current, images, overridePath, schema, target };
}

/** Run exactly the images of `record`, verify the running image IDs, then smoke. */
function restore(ctx, envFile, dir, record, overrideName) {
  for (const s of DEPLOYABLE) {
    const meta = inspectImage(ctx, record.images[s].tag);
    if (!meta || meta.id !== record.images[s].id) throw new Stop(`${record.images[s].tag} is missing or changed locally`);
  }
  const overridePath = writeOverride(dir, overrideName, record);
  docker(ctx, [...composeArgs(ctx, envFile, overridePath), 'up', '-d', '--no-build', '--pull', 'never', '--no-deps', '--wait', ...DEPLOYABLE], false);
  for (const s of DEPLOYABLE) if (runningContainer(ctx, envFile, overridePath, s).id !== record.images[s].id) throw Error(`${s} did not return to ${record.images[s].id}`);
  const smokeResult = smoke(ctx);
  ctx.log('LOCAL_DEV_SMOKE ' + smokeResult.checks.join(' '));
  return { overridePath, smoke: smokeResult };
}

export function apply(environmentArgument, backupArgument, options) {
  const ctx0 = defaults(options);
  const { environment } = loadEnv(ctx0, environmentArgument);
  return withLock(ctx0, stateDir(environment), 'apply', () => applyLocked(environmentArgument, backupArgument, options));
}
function applyLocked(environmentArgument, backupArgument, options) {
  const p = plan(environmentArgument, options);
  const { ctx, envFile, dir, sha, images, overridePath } = p;
  // The confirmed state to fall back to: the last confirmed local deploy, else the R6 baseline.
  const fallback = p.current ?? captureBaseline(ctx, envFile, dir);
  if (!p.current) {
    writeState(dir, 'current.json', fallback);
    writeState(dir, ACTIVE_OVERRIDE, overrideYaml(Object.fromEntries(DEPLOYABLE.map((s) => [s, fallback.images[s].tag]))));
  }
  if (p.target > p.schema) {
    if (!backupArgument) throw new Stop(`migration ${p.schema}->${p.target} requires --backup <fresh crane-backup-* set>`);
    verifyBackup(backupArgument, { source_sha: fallback.source_sha ?? '' }, p.schema, ctx.now());
  }
  assertExactCheckout(sha, ctx.root);
  const record = { schema_version: 1, kind: 'crane-local-deploy', project: productionProject, environment: devEnvironment, source_sha: sha,
    images: Object.fromEntries(DEPLOYABLE.map((s) => [s, { tag: images[s].tag, id: images[s].id, revision: images[s].revision, platform: images[s].platform }])), db_schema_version: p.target, override: overridePath,
    rollback: `node deploy/scripts/local-dev-deploy.mjs rollback <private.env>  → ${fallback.kind}:${fallback.source_sha || 'baseline'}` };
  const attempt = { ...record, status: 'IN_PROGRESS', fallback: { kind: fallback.kind, source_sha: fallback.source_sha }, started_at: ctx.now().toISOString() };
  writeState(dir, 'attempt.json', attempt);
  let reason = '';
  try {
    docker(ctx, [...composeArgs(ctx, envFile, overridePath), 'up', '-d', '--no-build', '--pull', 'never', '--no-deps', '--wait', ...DEPLOYABLE], false);
    for (const s of DEPLOYABLE) {
      const run = runningContainer(ctx, envFile, overridePath, s);
      if (run.id !== images[s].id) throw Error(`${s}: running image ${run.id} is not the built ${images[s].id}`);
      if (!images[s].carried && run.revision !== sha) throw Error(`${s}: running revision ${run.revision} is not ${sha}`);
    }
    if (dbSchema(ctx, envFile) !== p.target) throw Error('database schema after start differs from the source schema');
    const s = smoke(ctx);
    ctx.log('LOCAL_DEV_SMOKE ' + s.checks.join(' '));
    if (!s.ok) throw Object.assign(Error('smoke failed: ' + s.checks.filter((c) => c.endsWith('FAIL')).join(' ')), { code: 3 });
  } catch (error) {
    reason = error.message;
    // Not confirmed: current.json / active override still describe the fallback. Restore it when the
    // schema allows (no migration ran); otherwise STOP — image rollback after a migration is refused.
    const schemaNow = (() => { try { return dbSchema(ctx, envFile); } catch { return -1; } })();
    let restored = 'NOT_ATTEMPTED';
    if (schemaNow === fallback.db_schema_version) {
      try { restored = restore(ctx, envFile, dir, fallback, `override-restore-${fallback.source_sha || 'baseline'}.yml`).smoke.ok ? 'PASS' : 'SMOKE_FAIL'; }
      catch (e) { restored = 'FAIL: ' + e.message; }
    } else restored = `REFUSED: schema ${schemaNow} ≠ fallback ${fallback.db_schema_version} (restore from backup is a separate decision)`;
    writeState(dir, 'attempt.json', { ...attempt, status: 'FAILED', reason, restored, finished_at: ctx.now().toISOString() });
    ctx.log(`LOCAL_DEV_APPLY=FAIL source_sha=${sha} reason="${reason}" restore=${restored} fallback=${fallback.kind}:${fallback.source_sha || 'baseline'}`);
    const e = Error(`apply failed (${reason}); automatic restore of ${fallback.kind}: ${restored}`);
    e.code = restored === 'PASS' ? (error.code === 3 ? 3 : 1) : 2;
    throw e;
  }
  // Confirmed: only now become current; the confirmed fallback becomes previous.
  const confirmed = { ...record, deployed_at: ctx.now().toISOString() };
  writeState(dir, 'previous.json', fallback);
  writeState(dir, 'current.json', confirmed);
  writeState(dir, ACTIVE_OVERRIDE, readFileSync(overridePath, 'utf8'));
  writeState(dir, 'attempt.json', { ...attempt, status: 'CONFIRMED', finished_at: confirmed.deployed_at });
  ctx.log(`LOCAL_DEV_APPLY=PASS source_sha=${sha} previous=${fallback.kind}:${fallback.source_sha || 'baseline'} active_override=${join(dir, ACTIVE_OVERRIDE)}`);
  return confirmed;
}

/** Return to the recorded previous confirmed state (local deploy or the R6 baseline); same schema only. */
export function rollback(environmentArgument, options) {
  const ctx = defaults(options);
  const { envFile, environment } = loadEnv(ctx, environmentArgument);
  const dir = stateDir(environment);
  return withLock(ctx, dir, 'rollback', () => {
    const previous = readState(dir, 'previous.json');
    const current = readState(dir, 'current.json');
    if (!previous) throw new Stop('no previous confirmed deployment recorded');
    const schema = dbSchema(ctx, envFile);
    if (schema !== previous.db_schema_version) throw new Stop(`database schema ${schema} ≠ previous ${previous.db_schema_version}: image rollback refused (restore from backup is a separate decision)`);
    const { overridePath, smoke: s } = restore(ctx, envFile, dir, previous, `override-rollback-${previous.source_sha || 'baseline'}.yml`);
    writeState(dir, 'current.json', { ...previous, rolled_back_from: current?.source_sha, restored_at: ctx.now().toISOString() });
    writeState(dir, 'previous.json', current);
    writeState(dir, ACTIVE_OVERRIDE, readFileSync(overridePath, 'utf8'));
    if (!s.ok) { const e = Error('rollback completed but smoke failed; escalate'); e.code = 3; throw e; }
    ctx.log(`LOCAL_DEV_ROLLBACK=PASS to=${previous.kind}:${previous.source_sha || 'baseline'}`);
  });
}

export function status(environmentArgument, options) {
  const ctx = defaults(options);
  const { environment } = loadEnv(ctx, environmentArgument);
  const dir = stateDir(environment);
  const value = { current: readState(dir, 'current.json'), previous: readState(dir, 'previous.json'), attempt: readState(dir, 'attempt.json'), baseline: readState(dir, 'baseline.json'),
    activeOverride: existsSync(join(dir, ACTIVE_OVERRIDE)) ? join(dir, ACTIVE_OVERRIDE) : null };
  ctx.log(JSON.stringify(value, null, 2));
  return value;
}

/** The compose arguments every later operation must use: base + crane-dev overlay + ACTIVE override. */
export function activeComposeArgs(ctx, envFile, environment) {
  const path = join(stateDir(environment), ACTIVE_OVERRIDE);
  if (!existsSync(path)) throw new Stop(`${path} missing: no confirmed local deployment; maintenance would fall back to the registry images`);
  return composeArgs(ctx, envFile, path);
}

const FORBIDDEN_MAINTENANCE = new Set(['down', 'pull', 'push', 'build', 'rm', 'kill', 'create', 'convert']);
/** Maintenance passthrough (ps, logs, restart, exec, up --no-deps …) with the active override. */
export function composeMaintenance(environmentArgument, args, options) {
  const ctx = defaults(options);
  const { envFile, environment } = loadEnv(ctx, environmentArgument);
  const verb = args.find((a) => !a.startsWith('-'));
  if (!verb || FORBIDDEN_MAINTENANCE.has(verb) || args.includes('-v') || args.includes('--volumes') || (verb === 'up' && !args.includes('--no-deps')))
    throw Error('maintenance compose refuses down/pull/push/build/rm/kill/create, volume removal and `up` without --no-deps');
  const extra = verb === 'up' ? ['--no-build', '--pull', 'never'] : verb === 'run' ? ['--pull', 'never'] : [];
  const i = args.indexOf(verb);
  return docker(ctx, [...activeComposeArgs(ctx, envFile, environment), ...args.slice(0, i + 1), ...extra, ...args.slice(i + 1)], false);
}

/** Minimal structural check of a Fleet manifest (the Go validator runs in cloud-api at start). */
export function readFleet(path) {
  const m = JSON.parse(readFileSync(path, 'utf8'));
  // An explicit empty Fleet (devices: []) is legal for the Cloud (zero-business start).
  if (typeof m.tenantId !== 'string' || !Array.isArray(m.devices) || m.devices.length > 100) throw Error(`${path}: not a Fleet manifest (tenantId, 0..100 devices)`);
  const ids = new Set();
  for (const d of m.devices) {
    if (!/^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$/.test(d.runtimeDeviceId ?? '') || ids.has(d.runtimeDeviceId) || !['REAL', 'SIMULATION', 'TEST'].includes(d.sourceMode)) throw Error(`${path}: invalid or duplicate device ${d.runtimeDeviceId}`);
    ids.add(d.runtimeDeviceId);
  }
  return m;
}

/**
 * Replace the cloud Fleet and the broker ACL. Bind-mounted single files keep their inode, so the new
 * content is written IN PLACE (a rename would leave the containers on the old file); the previous
 * content is kept next to it. Then mosquitto re-reads the ACL (SIGHUP, connections stay) and
 * cloud-api is force-recreated with the active override (same image; it loads the Fleet at start).
 * The in-container content of both files is compared with what was written.
 */
export function updateFleet(environmentArgument, newFleet, newAcl, options) {
  const ctx = defaults(options);
  const { envFile, environment } = loadEnv(ctx, environmentArgument);
  const dir = stateDir(environment);
  return withLock(ctx, dir, 'fleet', () => {
    const fleetPath = environment.CRANE_FLEET_FILE, aclPath = environment.CRANE_MQTT_ACL;
    if (!fleetPath || !aclPath) throw Error('CRANE_FLEET_FILE and CRANE_MQTT_ACL must be set in the env file');
    const next = readFleet(newFleet), old = readFleet(fleetPath);
    if (next.tenantId !== old.tenantId) throw Error('tenant differs from the running Fleet');
    const keep = new Map(next.devices.map((d) => [d.runtimeDeviceId, d]));
    for (const d of old.devices) {
      const n = keep.get(d.runtimeDeviceId);
      if (!n) throw new Stop(`${d.runtimeDeviceId} would be removed from the Fleet; removal is a separate decision`);
      if (JSON.stringify(n) !== JSON.stringify(d)) throw new Stop(`${d.runtimeDeviceId} changed in the new Fleet; existing identities are immutable`);
    }
    const acl = readFileSync(newAcl, 'utf8');
    const users = [...acl.matchAll(/^user (\S+)$/gm)].map((m) => m[1]);
    const want = [...next.devices.map((d) => d.runtimeDeviceId), 'cloud-api'];
    if (users.length !== want.length || want.some((u) => !users.includes(u)) || /[#+]/.test(acl.replace(/^#.*$/gm, ''))) throw Error('ACL does not match the new Fleet exactly (one user per device + cloud-api, no wildcards)');
    const current = readState(dir, 'current.json');
    const args = activeComposeArgs(ctx, envFile, environment);
    const stamp = ctx.now().toISOString().replace(/[:.]/g, '-');
    for (const [target, source] of [[fleetPath, newFleet], [aclPath, newAcl]]) {
      const before = statSync(target);
      writeFileSync(`${target}.bak-${stamp}`, readFileSync(target), { mode: before.mode & 0o777 });
      writeFileSync(target, readFileSync(source)); // in place: same inode, mode and owner
      const after = statSync(target);
      if (after.ino !== before.ino || after.uid !== before.uid || (after.mode & 0o777) !== (before.mode & 0o777)) throw Error(`${target}: inode/owner/mode changed`);
    }
    docker(ctx, [...args, 'kill', '-s', 'SIGHUP', 'mosquitto']);
    docker(ctx, [...args, 'up', '-d', '--no-build', '--pull', 'never', '--no-deps', '--force-recreate', '--wait', 'cloud-api'], false);
    const inside = (svc, path) => sha256(docker(ctx, [...args, 'exec', '-T', svc, 'cat', path]));
    if (inside('mosquitto', '/mosquitto/acl') !== sha256(readFileSync(aclPath))) throw Error('ACL inside the mosquitto container differs from the written file');
    if (inside('cloud-api', '/inventory/fleet.json') !== sha256(readFileSync(fleetPath))) throw Error('Fleet inside the cloud-api container differs from the written file');
    if (current) {
      const run = runningContainer(ctx, envFile, join(dir, ACTIVE_OVERRIDE), 'cloud-api');
      if (run.id !== current.images['cloud-api'].id) throw Error('cloud-api was recreated with another image than the confirmed one');
    }
    const s = smoke(ctx);
    ctx.log('LOCAL_DEV_SMOKE ' + s.checks.join(' '));
    if (!s.ok) throw Object.assign(Error('smoke failed after the Fleet update; previous files kept as *.bak-' + stamp), { code: 3 });
    ctx.log(`LOCAL_DEV_FLEET=PASS devices=${next.devices.length} added=${next.devices.length - old.devices.length} acl_reloaded=SIGHUP cloud_api=recreated backup=*.bak-${stamp}`);
    return { added: next.devices.length - old.devices.length };
  });
}

const EMPTY_INVENTORY = (tenant) => JSON.stringify({ tenantId: tenant, projects: [], assetDevices: [], runtimeDevices: [] }, null, 2) + '\n';
const EMPTY_FLEET = (tenant) => JSON.stringify({ tenantId: tenant, devices: [] }, null, 2) + '\n';
const EMPTY_ACL = '# Generated development ACL: exact tenant/project/device scopes; not production PKI.\nuser cloud-api\n';
export const SIM_CONTAINER = 'u-dao-crane-ops-sim';
export const SIM_SPOOL_VOLUME = `${productionProject}_crane-simulation-spool`;

function writeInPlace(target, content, stamp) {
  const before = statSync(target);
  writeFileSync(`${target}.bak-${stamp}`, readFileSync(target), { mode: before.mode & 0o777 });
  writeFileSync(target, content);
  const after = statSync(target);
  if (after.ino !== before.ino || after.uid !== before.uid || (after.mode & 0o777) !== (before.mode & 0o777)) throw Error(`${target}: inode/owner/mode changed`);
}

/**
 * One-off crane-dev business reset (see header). `plan` is read-only; `apply` needs the plan digest,
 * an actor and --confirm crane-dev. The database step runs first and is all-or-nothing; the files are
 * replaced only after it committed; each step is verified.
 */
export function resetBusiness(environmentArgument, mode, { approve, actor, confirm } = {}, options) {
  const ctx = defaults(options);
  const { envFile, environment } = loadEnv(ctx, environmentArgument);
  const dir = stateDir(environment);
  return withLock(ctx, dir, 'reset-business', () => {
    const args = activeComposeArgs(ctx, envFile, environment);
    const current = readState(dir, 'current.json');
    if (current?.kind !== 'crane-local-deploy') throw new Stop('reset requires a confirmed local deployment (local-dev-deploy apply) first');
    const config = JSON.parse(docker(ctx, [...args, 'config', '--format', 'json']));
    const api = config.services['cloud-api'].environment;
    if (config.name !== productionProject || api.CRANE_DEPLOYMENT_ENVIRONMENT !== devEnvironment || api.PUBLIC_URL !== devOrigin || api.APP_ENV !== 'production')
      throw new Stop(`not the crane-dev instance (project ${config.name}, environment ${api.CRANE_DEPLOYMENT_ENVIRONMENT}, ${api.PUBLIC_URL})`);
    for (const key of ['CRANE_INVENTORY_FILE', 'CRANE_FLEET_FILE', 'CRANE_MQTT_ACL'])
      if (!environment[key] || !existsSync(environment[key])) throw Error(`${key} must point to the existing file`);
    const tool = (...a) => docker(ctx, [...args, 'run', '--rm', '--no-deps', '--pull', 'never', '--entrypoint', 'runtime-identity', 'cloud-api', 'reset-business', ...a]);
    if (mode === 'plan') {
      const out = JSON.parse(tool('plan'));
      writeState(dir, 'reset-plan.json', out.plan);
      ctx.log(`LOCAL_DEV_RESET_PLAN digest=${out.plan.digest} real=${out.plan.realRuntimes} delete=${JSON.stringify(out.plan.delete)} keep=${JSON.stringify(out.plan.keep)}`);
      return out.plan;
    }
    if (mode !== 'apply' || !approve || !actor || confirm !== devEnvironment) throw Error('apply requires --approve <plan digest> --actor <admin> --confirm crane-dev');
    const tenant = readFleet(environment.CRANE_FLEET_FILE).tenantId;
    // 1. Simulator off first (its spool would replay old events), then the database (one transaction).
    if (docker(ctx, ['ps', '-q', '--filter', `name=^${SIM_CONTAINER}$`]).trim()) docker(ctx, ['stop', '--time', '10', SIM_CONTAINER]);
    const applied = JSON.parse(tool('apply', '--approve', approve, '--actor', actor));
    // 2. Startup documents become explicit empty ones (in place: bind mounts keep their inode).
    const stamp = ctx.now().toISOString().replace(/[:.]/g, '-');
    writeInPlace(environment.CRANE_INVENTORY_FILE, EMPTY_INVENTORY(tenant), stamp);
    writeInPlace(environment.CRANE_FLEET_FILE, EMPTY_FLEET(tenant), stamp);
    writeInPlace(environment.CRANE_MQTT_ACL, EMPTY_ACL, stamp);
    // 3. Simulator spool (old queued events) removed; no simulator is running (step 1).
    if (docker(ctx, ['volume', 'ls', '-q', '--filter', `name=^${SIM_SPOOL_VOLUME}$`]).trim()) docker(ctx, ['volume', 'rm', SIM_SPOOL_VOLUME]);
    // 4. Broker re-reads the empty ACL; cloud-api restarts with the empty Fleet and empty Inventory.
    docker(ctx, [...args, 'kill', '-s', 'SIGHUP', 'mosquitto']);
    docker(ctx, [...args, 'up', '-d', '--no-build', '--pull', 'never', '--no-deps', '--force-recreate', '--wait', 'cloud-api'], false);
    const inside = (svc, path) => sha256(docker(ctx, [...args, 'exec', '-T', svc, 'cat', path]));
    if (inside('cloud-api', '/inventory/inventory.json') !== sha256(EMPTY_INVENTORY(tenant)) || inside('cloud-api', '/inventory/fleet.json') !== sha256(EMPTY_FLEET(tenant)) || inside('mosquitto', '/mosquitto/acl') !== sha256(EMPTY_ACL))
      throw Error('container files differ from the written empty documents');
    if (runningContainer(ctx, envFile, join(dir, ACTIVE_OVERRIDE), 'cloud-api').id !== current.images['cloud-api'].id) throw Error('cloud-api was recreated with another image than the confirmed one');
    const after = JSON.parse(tool('plan'));
    const left = Object.entries(after.plan.delete).filter(([, n]) => n !== 0);
    if (left.length) throw Error('business rows remain after reset: ' + JSON.stringify(Object.fromEntries(left)));
    const s = smoke(ctx);
    ctx.log('LOCAL_DEV_SMOKE ' + s.checks.join(' '));
    if (!s.ok) throw Object.assign(Error('smoke failed after the reset; previous files kept as *.bak-' + stamp), { code: 3 });
    writeState(dir, `reset-${stamp}.json`, { kind: 'crane-business-reset', at: ctx.now().toISOString(), actor, digest: approve, deleted: applied.deleted, kept: applied.kept, backups: `*.bak-${stamp}` });
    ctx.log(`LOCAL_DEV_RESET=PASS deleted=${JSON.stringify(applied.deleted)} inventory=EMPTY fleet=EMPTY acl=EMPTY spool=REMOVED cloud_api=recreated`);
    return applied;
  });
}

if (import.meta.main) {
  const [mode, envFile, ...rest] = process.argv.slice(2);
  const flag = (name) => { const i = rest.indexOf(name); return i >= 0 ? rest[i + 1] : undefined; };
  try {
    if (!envFile) throw Error('usage: local-dev-deploy.mjs build|plan|apply|rollback|status|compose|fleet <private.env> [--services a,b] [--goproxy list] [--backup dir] [--fleet f --acl a] [-- compose args]');
    if (mode === 'build') build(envFile, flag('--services') ? flag('--services').split(',') : undefined, undefined, flag('--goproxy'));
    else if (mode === 'plan') plan(envFile);
    else if (mode === 'apply') apply(envFile, flag('--backup'));
    else if (mode === 'rollback') rollback(envFile);
    else if (mode === 'status') status(envFile);
    else if (mode === 'compose') composeMaintenance(envFile, rest[0] === '--' ? rest.slice(1) : rest);
    else if (mode === 'fleet') updateFleet(envFile, flag('--fleet'), flag('--acl'));
    else if (mode === 'reset-business') resetBusiness(envFile, rest[0], { approve: flag('--approve'), actor: flag('--actor'), confirm: flag('--confirm') });
    else throw Error('unknown mode');
  } catch (error) { console.error(error.message); process.exitCode = error.code || 1; }
}
