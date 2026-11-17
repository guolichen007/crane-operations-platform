// Source-direct deployment for the crane-dev environment ONLY (https://crane.youdaozd.com,
// development stage). OWNER decision 2026-11-17: no registry images, no GHCR candidate, no dev
// tags, no OCI transport — the server builds the committed source locally with the reviewed
// Dockerfiles and runs those exact local images. The production release path (release.mjs,
// release-check.mjs, upgrade.mjs, dev-upgrade.mjs) is untouched and keeps its digest checks.
//
//   node deploy/scripts/local-dev-deploy.mjs build    <private.env> [--services cloud-api,web-console,device-simulator]
//   node deploy/scripts/local-dev-deploy.mjs plan     <private.env>
//   node deploy/scripts/local-dev-deploy.mjs apply    <private.env> [--backup <fresh crane-backup-* set>]
//   node deploy/scripts/local-dev-deploy.mjs rollback <private.env>
//   node deploy/scripts/local-dev-deploy.mjs status   <private.env>
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
//   - the previous and current versions plus the rollback command are recorded under
//     CRANE_LOCAL_DEPLOY_DIR (default /srv/u-dao/releases/crane-local), mode 0600;
//   - nothing is pushed; postgres / keycloak / mosquitto / other projects are never touched.
// Exit: 0 ok · 1 refused/error · 2 STOP (human decision) · 3 smoke failed.
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync, chmodSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { assertExactCheckout } from './source-check.mjs';
import { defaults, loadEnv, docker, smoke, dbSchema, Stop, verifyBackup } from './dev-upgrade.mjs';
import { productionProject, validateProduction, devEnvironment, devOrigin, devOverlay } from '../../tools/production-contract.mjs';

export const LOCAL_REPO = 'crane-local';
export const DEPLOYABLE = ['cloud-api', 'web-console'];
export const BUILDABLE = {
  'cloud-api': { dockerfile: 'deploy/Dockerfile.backend', args: [], inputs: ['go.mod', 'go.sum', 'contracts', 'internal', 'services', 'edge', 'tools/identity', 'tools/equipment-types', 'tools/runtime-identity', 'tools/provision-keycloak', 'deploy/postgres', 'deploy/Dockerfile.backend', 'licenses', 'THIRD_PARTY_NOTICES.md'] },
  'web-console': { dockerfile: 'deploy/Dockerfile.web-production', args: [], inputs: ['package.json', 'package-lock.json', 'apps/web-console', 'contracts', 'deploy/nginx/production.conf', 'deploy/Dockerfile.web-production', 'licenses', 'THIRD_PARTY_NOTICES.md'] },
  'device-simulator': { dockerfile: 'deploy/Dockerfile.backend', args: ['TARGET=simulators/device-simulator'], inputs: ['go.mod', 'go.sum', 'contracts', 'internal', 'edge', 'simulators', 'deploy/Dockerfile.backend'] },
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
  writeFileSync(join(dir, name), JSON.stringify(value, null, 2) + '\n', { mode: 0o600 });
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
export function build(environmentArgument, services = Object.keys(BUILDABLE), options) {
  const ctx = defaults(options);
  const { environment } = loadEnv(ctx, environmentArgument);
  const sha = git(ctx, 'rev-parse', 'HEAD');
  assertExactCheckout(sha, ctx.root);
  const out = {};
  for (const service of services) {
    const spec = BUILDABLE[service];
    if (!spec) throw Error('Unknown service ' + service);
    const tag = localTag(service, sha);
    const existing = inspectImage(ctx, tag);
    const scratch = `${LOCAL_REPO}/${service}:build-${sha.slice(0, 12)}-${process.pid}`;
    docker(ctx, ['build', '--pull=false', '--build-arg', `SOURCE_SHA=${sha}`, ...spec.args.flatMap((a) => ['--build-arg', a]), '-f', spec.dockerfile, '-t', scratch, '.'], false);
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
  writeState(dir, `build-${sha}.json`, { schema_version: 1, kind: 'crane-local-build', source_sha: sha, built_at: ctx.now().toISOString(), images: out });
  ctx.log(`LOCAL_DEV_BUILD=PASS source_sha=${sha} services=${services.join(',')} pushed=NONE`);
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

/** Read-only: what apply would do. */
export function plan(environmentArgument, options) {
  const ctx = defaults(options);
  const { envFile, environment } = loadEnv(ctx, environmentArgument);
  const sha = git(ctx, 'rev-parse', 'HEAD');
  assertExactCheckout(sha, ctx.root);
  const dir = stateDir(environment);
  const current = readState(dir, 'current.json');
  const images = {};
  for (const s of DEPLOYABLE) {
    const unchanged = current && current.source_sha !== sha && !changedServices(ctx, current.source_sha, sha, [s]).length && current.images?.[s];
    // A service whose inputs did not change keeps the exact image already running.
    const tag = unchanged ? current.images[s].tag : localTag(s, sha);
    const meta = inspectImage(ctx, tag);
    if (!meta) throw new Stop(`${tag} is not built yet: run build first`);
    images[s] = { tag, id: meta.id, revision: meta.revision, platform: `${meta.os}/${meta.architecture}`, carried: !!unchanged };
  }
  const overridePath = join(dir, `override-${sha}.yml`);
  mkdirSync(dir, { recursive: true, mode: 0o700 });
  writeFileSync(overridePath, overrideYaml(Object.fromEntries(DEPLOYABLE.map((s) => [s, images[s].tag]))), { mode: 0o600 });
  const config = JSON.parse(docker(ctx, [...composeArgs(ctx, envFile, overridePath), 'config', '--format', 'json']));
  // validateLocalDevelopment expects <svc>:<sha> for changed services; carried ones keep their tag.
  const forCheck = structuredClone(config);
  for (const s of DEPLOYABLE) if (images[s].carried) forCheck.services[s].image = localTag(s, sha);
  validateLocalDevelopment(forCheck, sha);
  const schema = dbSchema(ctx, envFile);
  const migrations = readdirSync(resolve(ctx.root, 'deploy/postgres/migrations')).map((f) => /^(\d{5})_/.exec(f)?.[1]).filter(Boolean).map(Number);
  const target = Math.max(...migrations);
  if (target < schema) throw new Stop(`source schema ${target} is older than database schema ${schema}`);
  ctx.log(`LOCAL_DEV_PLAN=PASS source_sha=${sha} previous=${current?.source_sha ?? 'none'} schema=${schema}->${target} migration=${target > schema ? 'YES(backup required)' : 'NO'} ` +
    DEPLOYABLE.map((s) => `${s}=${images[s].tag}${images[s].carried ? '(unchanged)' : ''}`).join(' '));
  return { ctx, envFile, environment, dir, sha, current, images, overridePath, schema, target };
}

export function apply(environmentArgument, backupArgument, options) {
  const p = plan(environmentArgument, options);
  const { ctx, envFile, dir, sha, current, images, overridePath } = p;
  if (p.target > p.schema) {
    if (!backupArgument) throw new Stop(`migration ${p.schema}->${p.target} requires --backup <fresh crane-backup-* set>`);
    verifyBackup(backupArgument, { source_sha: current?.source_sha ?? '' }, p.schema, ctx.now());
  }
  assertExactCheckout(sha, ctx.root);
  docker(ctx, [...composeArgs(ctx, envFile, overridePath), 'up', '-d', '--no-build', '--pull', 'never', '--no-deps', '--wait', ...DEPLOYABLE], false);
  for (const s of DEPLOYABLE) {
    const run = runningContainer(ctx, envFile, overridePath, s);
    if (run.id !== images[s].id) throw Error(`${s}: running image ${run.id} is not the built ${images[s].id}`);
    if (!images[s].carried && run.revision !== sha) throw Error(`${s}: running revision ${run.revision} is not ${sha}`);
  }
  if (dbSchema(ctx, envFile) !== p.target) throw Error('database schema after start differs from the source schema');
  const record = { schema_version: 1, kind: 'crane-local-deploy', project: productionProject, environment: devEnvironment, source_sha: sha, deployed_at: ctx.now().toISOString(),
    images: Object.fromEntries(DEPLOYABLE.map((s) => [s, { tag: images[s].tag, id: images[s].id, revision: images[s].revision, platform: images[s].platform }])), db_schema_version: p.target, override: overridePath,
    rollback: current ? `git checkout ${current.source_sha} && node deploy/scripts/local-dev-deploy.mjs rollback <private.env>` : 'none (first local deployment; previous registry release: dev-upgrade.mjs rollback)' };
  if (current) writeState(dir, 'previous.json', current);
  writeState(dir, 'current.json', record);
  const s = smoke(ctx);
  ctx.log('LOCAL_DEV_SMOKE ' + s.checks.join(' '));
  if (!s.ok) { const e = Error('smoke failed; ' + record.rollback); e.code = 3; throw e; }
  ctx.log(`LOCAL_DEV_APPLY=PASS source_sha=${sha} previous=${current?.source_sha ?? 'none'} rollback="${record.rollback}"`);
  return record;
}

/** Return to the recorded previous local deployment (same schema only). */
export function rollback(environmentArgument, options) {
  const ctx = defaults(options);
  const { envFile, environment } = loadEnv(ctx, environmentArgument);
  const dir = stateDir(environment);
  const previous = readState(dir, 'previous.json');
  const current = readState(dir, 'current.json');
  if (!previous) throw new Stop('no previous local deployment recorded');
  const schema = dbSchema(ctx, envFile);
  if (schema !== previous.db_schema_version) throw new Stop(`database schema ${schema} ≠ previous ${previous.db_schema_version}: image rollback refused (restore from backup is a separate decision)`);
  for (const s of DEPLOYABLE) {
    const meta = inspectImage(ctx, previous.images[s].tag);
    if (!meta || meta.id !== previous.images[s].id) throw new Stop(`${previous.images[s].tag} is missing or changed locally`);
  }
  const overridePath = join(dir, `override-rollback-${previous.source_sha}.yml`);
  writeFileSync(overridePath, overrideYaml(Object.fromEntries(DEPLOYABLE.map((s) => [s, previous.images[s].tag]))), { mode: 0o600 });
  docker(ctx, [...composeArgs(ctx, envFile, overridePath), 'up', '-d', '--no-build', '--pull', 'never', '--no-deps', '--wait', ...DEPLOYABLE], false);
  for (const s of DEPLOYABLE) if (runningContainer(ctx, envFile, overridePath, s).id !== previous.images[s].id) throw Error(`${s} did not return to the previous image`);
  writeState(dir, 'current.json', { ...previous, rolled_back_from: current?.source_sha, deployed_at: ctx.now().toISOString() });
  writeState(dir, 'previous.json', current);
  const s = smoke(ctx);
  ctx.log('LOCAL_DEV_SMOKE ' + s.checks.join(' '));
  if (!s.ok) { const e = Error('rollback completed but smoke failed; escalate'); e.code = 3; throw e; }
  ctx.log(`LOCAL_DEV_ROLLBACK=PASS source_sha=${previous.source_sha}`);
}

export function status(environmentArgument, options) {
  const ctx = defaults(options);
  const { environment } = loadEnv(ctx, environmentArgument);
  const dir = stateDir(environment);
  const value = { current: readState(dir, 'current.json'), previous: readState(dir, 'previous.json') };
  ctx.log(JSON.stringify(value, null, 2));
  return value;
}

if (import.meta.main) {
  const [mode, envFile, ...rest] = process.argv.slice(2);
  const flag = (name) => { const i = rest.indexOf(name); return i >= 0 ? rest[i + 1] : undefined; };
  try {
    if (!envFile) throw Error('usage: local-dev-deploy.mjs build|plan|apply|rollback|status <private.env> [--services a,b] [--backup dir]');
    if (mode === 'build') build(envFile, flag('--services') ? flag('--services').split(',') : undefined);
    else if (mode === 'plan') plan(envFile);
    else if (mode === 'apply') apply(envFile, flag('--backup'));
    else if (mode === 'rollback') rollback(envFile);
    else if (mode === 'status') status(envFile);
    else throw Error('unknown mode');
  } catch (error) { console.error(error.message); process.exitCode = error.code || 1; }
}
