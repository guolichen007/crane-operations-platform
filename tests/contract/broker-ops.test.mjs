// R9 A3: broker status / preflight / controlled recovery against a fake Docker. Proves: the source
// pins 2.0.23 with a healthcheck and unless-stopped; drift (2.0.22, no healthcheck, other policy) is
// reported and recovered by recreating ONLY mosquitto; a healthy broker is not restarted without
// --force; recovery evidence is private; the gate refuses an unhealthy dependency.
import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync, readFileSync, readdirSync, statSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import YAML from 'yaml';
import { status, preflight, recover, dependencyGate, pinnedBroker, RESTART_POLICY } from '../../deploy/scripts/broker-ops.mjs';
import { ACTIVE_OVERRIDE } from '../../deploy/scripts/local-dev-deploy.mjs';

const root = process.cwd();
const PINNED = pinnedBroker(root);

test('source: broker pinned to 2.0.23 by digest, healthcheck defined, restart unless-stopped, cloud-api waits for healthy', () => {
  assert.match(PINNED, /^eclipse-mosquitto:2\.0\.23@sha256:[a-f0-9]{64}$/);
  const c = YAML.parse(readFileSync('deploy/compose/docker-compose.prod.yml', 'utf8'));
  const m = c.services.mosquitto;
  assert.equal(m.image, PINNED, 'compose and images.json pin the same broker');
  assert.equal(m.restart ?? c['x-security']?.restart, RESTART_POLICY);
  assert.deepEqual(m.healthcheck.test.slice(0, 2), ['CMD', 'nc']);
  assert.equal(c.services['cloud-api'].depends_on.mosquitto.condition, 'service_healthy');
  // mTLS / ACL / persistence semantics unchanged.
  const conf = readFileSync('deploy/mosquitto/mosquitto.prod.conf', 'utf8');
  for (const line of ['require_certificate true', 'use_identity_as_username true', 'allow_anonymous false', 'acl_file /mosquitto/acl', 'persistence true', 'tls_version tlsv1.2'])
    assert.ok(conf.includes(line), line);
  assert.ok(m.volumes.includes('crane-mqtt:/mosquitto/data'));
});

function fixture(t, over = {}) {
  const dir = mkdtempSync(join(tmpdir(), 'broker-ops-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const state = join(dir, 'state');
  const env = join(dir, 'crane.env');
  writeFileSync(env, `CRANE_LOCAL_DEPLOY_DIR=${state}\n`, { mode: 0o600 });


  const svc = {
    postgres: { image: 'postgres@sha256:' + 'a'.repeat(64), health: 'healthy', running: true, policy: RESTART_POLICY },
    keycloak: { image: 'keycloak@sha256:' + 'b'.repeat(64), health: 'healthy', running: true, policy: RESTART_POLICY },
    mosquitto: { image: 'eclipse-mosquitto:2.0.22@sha256:' + '1'.repeat(64), health: 'none', running: false, status: 'exited', exit: 134, restarts: 0, policy: RESTART_POLICY, hc: null },
    ...over,
  };
  const calls = [];
  let cloudConnected = 0;
  const execute = (cmd, args) => {
    calls.push(args.join(' '));
    if (args[0] === 'compose') {
      const svcName = args.at(-1);
      if (args.includes('ps')) return { status: 0, stdout: 'c-' + svcName + '\n' };
      if (args.includes('logs')) return { status: 0, stdout: 'mosquitto: database.c:551: assert failed\n' };
      if (args.includes('exec') && args.includes('wget')) return { status: 0, stdout: `mqtt_connected ${cloudConnected}\nmqtt_connect_failures_total 7\nmqtt_down_seconds ${cloudConnected ? 0 : 42}\nmqtt_reconnects_total 2\n` };
      if (args.includes('exec') && args.includes('test')) return { status: 0, stdout: '' };
      if (args.includes('up')) {
        assert.ok(args.includes('--no-deps') && args.includes('never') && args.includes('--no-build'));
        assert.equal(svcName, 'mosquitto', 'only the broker is recreated');
        Object.assign(svc.mosquitto, { image: PINNED, health: 'healthy', running: true, status: 'running', hc: ['CMD', 'nc', '-z'], exit: 0 });
        cloudConnected = 1;
        return { status: 0, stdout: '' };
      }
      if (args.includes('restart')) {
        assert.equal(svcName, 'mosquitto');
        Object.assign(svc.mosquitto, { health: 'healthy', running: true, status: 'running' }); cloudConnected = 1; return { status: 0, stdout: '' };
      }
    }
    if (args[0] === 'inspect') {
      const name = args.at(-1).slice(2); const v = svc[name];
      return { status: 0, stdout: JSON.stringify({ Id: 'id-' + name, Image: 'sha256:' + 'c'.repeat(64), Config: { Image: v.image, Labels: { 'com.docker.compose.project': 'u-dao-crane-ops' }, Healthcheck: v.hc === undefined ? { Test: ['CMD', 'x'] } : v.hc ? { Test: v.hc } : undefined },
        State: { Running: v.running, Status: v.status ?? 'running', ExitCode: v.exit ?? 0, OOMKilled: false, Health: v.health === 'none' ? undefined : { Status: v.health, Log: [] } },
        HostConfig: { RestartPolicy: { Name: v.policy } }, RestartCount: v.restarts ?? 0,
        Mounts: name === 'mosquitto' ? [{ Type: 'volume', Destination: '/mosquitto/data', Name: 'u-dao-crane-ops_crane-mqtt', RW: true }] : [] }) };
    }
    throw Error('unexpected docker ' + args.join(' '));
  };
  const logs = [];
  return { env, state, svc, calls, logs, opts: { root, execute, log: (l) => logs.push(l), now: () => new Date('2026-11-19T10:00:00Z'), sleep: () => {} } };
}

test('status reports drift; gate refuses an exited broker; recover recreates only mosquitto to the pinned image and keeps evidence private', async (t) => {
  const fs = await import('node:fs');
  const f = fixture(t);
  fs.mkdirSync(f.state, { recursive: true });
  writeFileSync(join(f.state, ACTIVE_OVERRIDE), 'services: {}\n', { mode: 0o600 });
  const s = status(f.env, f.opts);
  assert.equal(s.imageMatchesSource, false);
  assert.equal(s.healthcheck.defined, false);
  assert.equal(s.cloud.connected, 0);
  assert.equal(s.cloud.connectFailures, 7);
  assert.throws(() => dependencyGate(f.opts, ['compose']), /mosquitto\(exited/);
  assert.throws(() => preflight(f.env, f.opts), (e) => e.code === 3 && /mosquitto_healthy/.test(e.message));
  assert.throws(() => recover(f.env, {}, f.opts), /--reason/);
  const r = recover(f.env, { reason: 'broker exited with assert (134)', actor: 'ops' }, f.opts);
  assert.equal(r.action, 'recreate');
  assert.equal(f.svc.mosquitto.image, PINNED);
  assert.ok(!f.calls.some((c) => /\bup\b.*(cloud-api|postgres|keycloak|web-console)$/.test(c)), 'no other service touched');
  const verbs = f.calls.filter((c) => c.startsWith('compose')).map((c) => c.split(' ').find((w, i, a) => ['up', 'down', 'rm', 'pull', 'restart', 'kill', 'stop', 'ps', 'logs', 'exec'].includes(w) && a[i - 1] !== '--pull'));
  assert.ok(!verbs.some((v) => ['down', 'rm', 'pull', 'kill', 'stop'].includes(v)), 'no down / rm / pull / kill / stop: ' + verbs.join(','));
  assert.ok(!f.calls.some((c) => c.startsWith('volume')), 'no volume operation');
  const files = readdirSync(join(f.state, 'broker'));
  assert.equal(files.length, 2);
  for (const n of files) assert.equal(statSync(join(f.state, 'broker', n)).mode & 0o777, 0o600);
  const evidence = JSON.parse(readFileSync(join(f.state, 'broker', files.find((n) => n.endsWith('.json'))), 'utf8'));
  assert.equal(evidence.before.exitCode, 134);
  assert.equal(evidence.result, 'PASS');
  assert.ok(f.logs.some((l) => l.startsWith('BROKER_RECOVERY=PASS action=recreate')));
  // Now healthy + pinned: preflight passes; a second recover is refused without --force.
  assert.equal(preflight(f.env, f.opts).ok, true);
  assert.throws(() => recover(f.env, { reason: 'again' }, f.opts), /nothing to recover/);
  const forced = recover(f.env, { reason: 'operator check', force: true }, f.opts);
  assert.equal(forced.action, 'restart');
});
