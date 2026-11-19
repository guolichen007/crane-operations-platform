// R9 A3 — Mosquitto broker health, monitoring and controlled recovery for the crane-dev deployment
// (project u-dao-crane-ops). Read-only by default; the only write is a controlled restart of the
// mosquitto service itself (never another service, never another Compose project).
//
//   node deploy/scripts/broker-ops.mjs status    <private.env>
//            read-only: source vs running image digest, restart policy, healthcheck, Health state,
//            RestartCount / OOMKilled / ExitCode, persistence volume, cloud-api broker metrics
//   node deploy/scripts/broker-ops.mjs preflight <private.env>
//            read-only dependency gate before any deploy: postgres + keycloak + mosquitto healthy,
//            mosquitto image == pinned source digest, restart policy unless-stopped, ACL + broker
//            certificate files readable inside the container, cloud-api MQTT connected (if running)
//   node deploy/scripts/broker-ops.mjs recover   <private.env> --reason "<text>" [--actor <admin>]
//            controlled recovery: refuses while healthy unless --force; records the pre-state
//            (RestartCount, ExitCode, OOMKilled, last 200 log lines → private state dir, mode 0600);
//            `up -d --no-deps --no-build --pull never --wait mosquitto` (recreates only when the
//            definition drifted, e.g. image upgrade / restart policy) else `restart mosquitto`;
//            then waits for Health=healthy AND cloud-api mqtt_connected=1; the persistence volume,
//            mTLS material, ACL and message semantics are untouched.
//
// Exit: 0 ok · 1 refused/error · 2 STOP (human decision) · 3 dependency unhealthy.
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { defaults, loadEnv, docker, Stop } from './dev-upgrade.mjs';
import { activeComposeArgs, stateDir } from './local-dev-deploy.mjs';
import { productionProject } from '../../tools/production-contract.mjs';

export const BROKER = 'mosquitto';
export const DEPENDENCIES = ['postgres', 'keycloak', 'mosquitto'];
export const RESTART_POLICY = 'unless-stopped';

/** Pinned broker image from the source (deploy/production/images.json). */
export function pinnedBroker(root) {
  return JSON.parse(readFileSync(join(root, 'deploy/production/images.json'), 'utf8')).mosquitto;
}

function containerOf(ctx, args, service) {
  const ids = docker(ctx, [...args, 'ps', '-a', '-q', service]).trim().split('\n').filter(Boolean);
  if (ids.length !== 1) throw new Stop(`${service} must have exactly one container in ${productionProject} (found ${ids.length})`);
  return ids[0];
}

/** Docker state of one service container (no environment, no secrets). */
export function inspectService(ctx, args, service) {
  const id = containerOf(ctx, args, service);
  const v = JSON.parse(docker(ctx, ['inspect', '--format', '{{json .}}', id]));
  const labels = v.Config?.Labels ?? {};
  if (labels['com.docker.compose.project'] !== productionProject) throw new Stop(`${service} container belongs to ${labels['com.docker.compose.project']}, not ${productionProject}`);
  return {
    service, id: v.Id?.slice(0, 12) ?? id,
    image: v.Config?.Image ?? '', imageId: v.Image ?? '',
    running: !!v.State?.Running, status: v.State?.Status ?? '', health: v.State?.Health?.Status ?? 'none',
    healthLog: (v.State?.Health?.Log ?? []).slice(-3).map((h) => ({ exitCode: h.ExitCode, end: h.End })),
    restartPolicy: v.HostConfig?.RestartPolicy?.Name ?? '', restartCount: v.RestartCount ?? 0,
    exitCode: v.State?.ExitCode ?? 0, oomKilled: !!v.State?.OOMKilled, startedAt: v.State?.StartedAt ?? '', finishedAt: v.State?.FinishedAt ?? '',
    healthcheck: v.Config?.Healthcheck?.Test ?? null,
    mounts: (v.Mounts ?? []).map((m) => ({ type: m.Type, destination: m.Destination, name: m.Name ?? '', rw: m.RW })),
  };
}

/** cloud-api broker metrics from inside its container (no network exposure needed). */
export function cloudMetrics(ctx, args) {
  let text = '';
  try { text = docker(ctx, [...args, 'exec', '-T', 'cloud-api', 'wget', '-qO-', 'http://127.0.0.1:8081/metrics']); } catch { return null; }
  const read = (name) => { const m = new RegExp(`^${name} (\\d+)$`, 'm').exec(text); return m ? Number(m[1]) : null; };
  return { connected: read('mqtt_connected'), connectFailures: read('mqtt_connect_failures_total'), downSeconds: read('mqtt_down_seconds'), reconnects: read('mqtt_reconnects_total') };
}

export function status(environmentArgument, options) {
  const ctx = defaults(options);
  const { envFile, environment } = loadEnv(ctx, environmentArgument);
  const args = activeComposeArgs(ctx, envFile, environment);
  const pinned = pinnedBroker(ctx.root);
  const broker = inspectService(ctx, args, BROKER);
  const out = {
    pinned, running: broker.image, imageMatchesSource: broker.image === pinned,
    restartPolicy: { running: broker.restartPolicy, source: RESTART_POLICY, matches: broker.restartPolicy === RESTART_POLICY },
    healthcheck: { running: broker.healthcheck, defined: Array.isArray(broker.healthcheck) && broker.healthcheck[0] !== 'NONE' },
    broker, cloud: cloudMetrics(ctx, args),
  };
  ctx.log('BROKER_STATUS ' + JSON.stringify(out));
  return out;
}

/**
 * Gate used by local-dev-deploy apply BEFORE any container changes: postgres, keycloak and mosquitto
 * must be running and healthy (a definition without a healthcheck — the pre-R9 broker — must at
 * least be running with zero restarts since start). It never requires the pinned broker image, so an
 * application deploy is not blocked by a pending broker upgrade (that is `recover`'s job).
 */
export function dependencyGate(ctx, args) {
  const bad = [];
  for (const s of DEPENDENCIES) {
    const v = inspectService(ctx, args, s);
    const healthy = v.running && (v.health === 'healthy' || (v.health === 'none' && v.status === 'running'));
    if (!healthy) bad.push(`${s}(${v.status}/${v.health}, restarts=${v.restartCount}, exit=${v.exitCode}${v.oomKilled ? ', OOM' : ''})`);
  }
  ctx.log('DEPENDENCY_GATE ' + (bad.length ? 'FAIL ' + bad.join(' ') : 'PASS ' + DEPENDENCIES.join(',')));
  if (bad.length) throw Object.assign(new Stop('dependencies not healthy before deploy: ' + bad.join(', ') + ' — recover them first (broker-ops.mjs recover for mosquitto)'), { code: 3 });
}

/** Dependency gate run before every deploy (read-only). Throws Stop (code 3) on any failure. */
export function preflight(environmentArgument, options) {
  const ctx = defaults(options);
  const { envFile, environment } = loadEnv(ctx, environmentArgument);
  const args = activeComposeArgs(ctx, envFile, environment);
  const checks = [];
  const check = (name, ok, detail = '') => { checks.push({ name, ok, detail }); return ok; };
  for (const s of DEPENDENCIES) {
    const v = inspectService(ctx, args, s);
    check(`${s}_running`, v.running, v.status);
    check(`${s}_healthy`, v.health === 'healthy', v.health);
    check(`${s}_restart_policy`, v.restartPolicy === RESTART_POLICY, v.restartPolicy);
    if (s === BROKER) {
      check('mosquitto_image_pinned', v.image === pinnedBroker(ctx.root), v.image);
      check('mosquitto_persistence_volume', v.mounts.some((m) => m.type === 'volume' && m.destination === '/mosquitto/data' && m.rw), '/mosquitto/data');
      for (const f of ['/mosquitto/acl', '/mosquitto/certs/ca.crt', '/mosquitto/certs/mosquitto.crt', '/mosquitto/certs/mosquitto.key']) {
        let ok = true;
        try { docker(ctx, [...args, 'exec', '-T', BROKER, 'test', '-r', f]); } catch { ok = false; }
        check(`mosquitto_readable:${f}`, ok);
      }
    }
  }
  const cloud = cloudMetrics(ctx, args);
  if (cloud) check('cloud_api_mqtt_connected', cloud.connected === 1, JSON.stringify(cloud));
  const failed = checks.filter((c) => !c.ok);
  ctx.log('BROKER_PREFLIGHT ' + checks.map((c) => `${c.name}=${c.ok ? 'PASS' : 'FAIL'}`).join(' '));
  if (failed.length) throw Object.assign(new Stop('dependency preflight failed: ' + failed.map((c) => `${c.name}(${c.detail})`).join(', ')), { code: 3 });
  return { ok: true, checks };
}

/** Controlled broker recovery (see header). */
export function recover(environmentArgument, { reason, actor = '', force = false } = {}, options) {
  const ctx = defaults(options);
  const { envFile, environment } = loadEnv(ctx, environmentArgument);
  if (!reason || reason.trim().length < 3 || reason.length > 200) throw Error('--reason "<what happened>" required (3..200 chars)');
  const args = activeComposeArgs(ctx, envFile, environment);
  const before = inspectService(ctx, args, BROKER);
  const pinned = pinnedBroker(ctx.root);
  const drift = before.image !== pinned || before.restartPolicy !== RESTART_POLICY || !Array.isArray(before.healthcheck) || before.healthcheck[0] === 'NONE';
  if (before.running && before.health === 'healthy' && !drift && !force)
    throw new Stop('mosquitto is running and healthy with the source definition; nothing to recover (use --force only with a documented reason)');
  // Evidence first (private state dir): container state + last 200 log lines (broker logs carry no secrets).
  const dir = join(stateDir(environment), 'broker');
  mkdirSync(dir, { recursive: true, mode: 0o700 });
  const stamp = ctx.now().toISOString().replace(/[:.]/g, '-');
  let logs = '';
  try { logs = docker(ctx, [...args, 'logs', '--no-color', '--tail', '200', BROKER]); } catch { logs = '(logs unavailable)'; }
  writeFileSync(join(dir, `recover-${stamp}.json`), JSON.stringify({ reason, actor, force, drift, before, pinned }, null, 2) + '\n', { mode: 0o600 });
  writeFileSync(join(dir, `recover-${stamp}.log`), logs, { mode: 0o600 });
  // Definition drift (image upgrade, restart policy, healthcheck) needs a recreate; otherwise restart.
  if (drift) docker(ctx, [...args, 'up', '-d', '--no-deps', '--no-build', '--pull', 'never', '--wait', '--wait-timeout', '120', BROKER], false);
  else docker(ctx, [...args, 'restart', '-t', '20', BROKER], false);
  let after = inspectService(ctx, args, BROKER);
  for (let i = 0; i < 24 && after.health !== 'healthy'; i++) { ctx.sleep?.(5000); after = inspectService(ctx, args, BROKER); }
  let cloud = cloudMetrics(ctx, args);
  for (let i = 0; i < 24 && cloud && cloud.connected !== 1; i++) { ctx.sleep?.(5000); cloud = cloudMetrics(ctx, args); }
  const ok = after.health === 'healthy' && after.image === pinned && after.restartPolicy === RESTART_POLICY && (!cloud || cloud.connected === 1);
  writeFileSync(join(dir, `recover-${stamp}.json`), JSON.stringify({ reason, actor, force, drift, before, after, cloud, pinned, result: ok ? 'PASS' : 'FAIL' }, null, 2) + '\n', { mode: 0o600 });
  ctx.log(`BROKER_RECOVERY=${ok ? 'PASS' : 'FAIL'} action=${drift ? 'recreate' : 'restart'} image=${after.image} health=${after.health} restart_policy=${after.restartPolicy} cloud_mqtt_connected=${cloud?.connected ?? 'n/a'} evidence=${join(dir, `recover-${stamp}.json`)}`);
  if (!ok) throw Object.assign(Error('broker did not return to healthy + cloud connected; evidence kept'), { code: 3 });
  return { action: drift ? 'recreate' : 'restart', before, after, cloud };
}

if (import.meta.main) {
  const [mode, envFile, ...rest] = process.argv.slice(2);
  const flag = (name) => { const i = rest.indexOf(name); return i >= 0 ? rest[i + 1] : undefined; };
  const sleep = (ms) => Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
  try {
    if (!envFile) throw Error('usage: broker-ops.mjs status|preflight|recover <private.env> [--reason text] [--actor id] [--force]');
    if (mode === 'status') status(envFile, { sleep });
    else if (mode === 'preflight') preflight(envFile, { sleep });
    else if (mode === 'recover') recover(envFile, { reason: flag('--reason'), actor: flag('--actor'), force: rest.includes('--force') }, { sleep });
    else throw Error('unknown mode');
  } catch (error) { console.error(error.message); process.exitCode = error.code || 1; }
}
