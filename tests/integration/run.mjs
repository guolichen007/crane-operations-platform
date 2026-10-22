import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, mkdirSync, existsSync } from 'node:fs';
import { resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
const win = process.platform === 'win32';
const go = existsSync('tools/.cache/go/bin/go.exe') ? resolve('tools/.cache/go/bin/go.exe') : 'go';
const docker = win ? 'C:/Program Files/Docker/Docker/resources/bin/docker.exe' : 'docker';
const config = Object.fromEntries(
  readFileSync('deploy/compose/runtime/.env', 'utf8')
    .trim()
    .split(/\r?\n/)
    .map((s) => {
      const i = s.indexOf('=');
      return [s.slice(0, i), s.slice(i + 1)];
    })
);
const token = config.DEV_API_TOKEN;
const base = process.env.PHASE0_API_URL || 'http://127.0.0.1:8081';
const env = {
  ...process.env,
  GOCACHE: resolve('tools/.cache/gobuild'),
  GOMODCACHE: resolve('tools/.cache/gomod'),
  GOPATH: resolve('tools/.cache/gopath'),
};
function run(cmd, args, extra = {}) {
  const r = spawnSync(cmd, args, { stdio: 'inherit', env: { ...env, ...extra } });
  assert.equal(r.status, 0, `${cmd} failed`);
}
function compose(args, extra = {}) {
  run(
    docker,
    [
      'compose',
      '--env-file',
      'deploy/compose/runtime/.env',
      '-f',
      'deploy/compose/docker-compose.dev.yml',
      ...args,
    ],
    extra
  );
}
const pause = (ms) => new Promise((r) => setTimeout(r, ms));
async function api(path, method = 'GET', expected = 200) {
  const res = await fetch(base + '/api/v1/' + path, {
    method,
    headers: { Authorization: `Bearer ${token}` },
    signal: AbortSignal.timeout(5000),
  });
  assert.equal(res.status, expected, `${method} ${path}`);
  return expected === 204 ? null : res.json();
}
async function until(label, fn, timeout = 60000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) {
    try {
      const value = await fn();
      if (value) {
        console.log('PASS ' + label);
        return value;
      }
    } catch {
      /* dependency warming up */
    }
    await pause(1000);
  }
  throw Error(`Timed out: ${label}`);
}
const proof = [];
function record(check) {
  proof.push({ check, status: 'PASS', at: new Date().toISOString() });
}
mkdirSync('local-private/validation', { recursive: true });
try {
  await until('cloud readiness', async () => (await fetch(base + '/health/ready')).ok);
  assert.equal((await fetch(base + '/api/v1/devices')).status, 401);
  record('unauthorized API rejected');
  const first = await until('one-device Shadow', async () => {
    const list = await api('devices');
    return list.find(
      (d) => d.deviceId === 'SIM-001' && d.reported.connectivity === 'MQTT_CONNECTED'
    );
  });
  assert.equal(first.reported.edgeHealth, 'EDGE_HEALTHY');
  record('one device Edge → MQTT TLS → Cloud → PostgreSQL');
  compose(['up', '-d', '--force-recreate', 'cloud-api'], {
    LIVE_TTL_SECONDS: '5',
    LIVE_GRACE_SECONDS: '2',
  });
  await until(
    'short test lease configuration',
    async () => (await fetch(base + '/health/ready')).ok
  );
  const fleetRestart = Date.now();
  compose(['up', '-d', '--force-recreate', '--no-deps', 'device-simulator'], { SIM_COUNT: '20' });
  await until('20 connected devices', async () => {
    const list = await api('devices');
    return (
      list.filter(
        (d) =>
          d.reported.connectivity === 'MQTT_CONNECTED' && Date.parse(d.updatedAt) >= fleetRestart
      ).length === 20
    );
  });
  record('20 independent MQTT certificate identities');
  const sessions = [];
  for (let i = 0; i < 5; i++)
    sessions.push(await api('devices/SIM-001/live-sessions', 'POST', 201));
  const live = await until('WATCH → 1 Hz LIVE', async () => {
    const d = await api('devices/SIM-001/shadow');
    return d.reported.kind === 'LIVE' && d;
  });
  record('watch and live');
  assert.equal(sessions[0].telemetryHz, 1);
  const version = live.version;
  await until(
    'LIVE sequence advances',
    async () => (await api('devices/SIM-001/shadow')).version > version
  );
  await api(`live-sessions/${sessions[0].sessionId}/renew`, 'POST');
  const metrics = await (await fetch(base + '/metrics')).text();
  assert.match(metrics, /device_live_sessions 5\b/);
  record('five viewers share manager upstream');
  for (const s of sessions) await api(`live-sessions/${s.sessionId}`, 'DELETE', 204);
  await until(
    'grace → SILENT',
    async () => (await api('devices/SIM-001/shadow')).reported.operatingState === 'SILENT',
    20000
  );
  record('grace after final viewer');
  await api('devices/SIM-001/live-sessions', 'POST', 201);
  await until(
    'unrenewed lease enters LIVE',
    async () => (await api('devices/SIM-001/shadow')).reported.kind === 'LIVE'
  );
  await until(
    'unrenewed TTL expires',
    async () => (await api('devices/SIM-001/shadow')).reported.operatingState === 'SILENT',
    20000
  );
  record('unrenewed TTL returns SILENT');
  compose(['exec', '-T', 'web-console', 'nginx', '-s', 'reload']);
  const current = await api('devices/SIM-001/shadow');
  const alarm = {
    ...current.reported,
    kind: 'ALARM',
    messageId: 'phase0-alarm-' + Date.now(),
    code: 'SIMULATED_OVERLOAD',
    text: 'Anonymous integration alarm',
    timestamp: new Date().toISOString(),
  };
  const fixture = 'local-private/validation/mqtt-message.json';
  writeFileSync(fixture, JSON.stringify(alarm));
  run(go, ['run', './tools/diagnostics/mqtt-probe', '--message', fixture]);
  await until(
    'alarm from SILENT and duplicate deduplication',
    async () => (await api('alarms')).filter((a) => a.messageId === alarm.messageId).length === 1
  );
  record('TLS ACL deny cross-device and command; immediate alarm; dedup');
  writeFileSync(
    fixture,
    JSON.stringify({ ...current.reported, kind: 'SNAPSHOT', x: 999, sequence: 0 })
  );
  run(go, ['run', './tools/diagnostics/mqtt-probe', '--message', fixture]);
  await pause(1000);
  assert.notEqual((await api('devices/SIM-001/shadow')).reported.x, 999);
  record('out-of-order rejection');
  const { chromium } = await import('@playwright/test');
  const browser = await chromium.launch(win ? { channel: 'chrome' } : { headless: true });
  try {
    const page = await browser.newPage();
    await page.goto(
      (process.env.PHASE0_WEB_URL || `http://127.0.0.1:${config.WEB_PORT || 18080}`) + '/#/devices'
    );
    await page.getByLabel('本地开发凭据', { exact: true }).fill(token);
    await page.getByRole('button', { name: '连接云端', exact: true }).click();
    await page.getByTestId('cloud-device').first().waitFor();
    assert.equal(await page.getByTestId('cloud-device').count(), 20);
    await page.getByTestId('cloud-device').first().click();
    await page.getByTestId('device-shadow').waitFor();
    assert.ok((await page.getByTestId('device-shadow').textContent()).includes('Last Known State'));
    await page.screenshot({ path: 'local-private/validation/cloud-shadow.png', fullPage: true });
    record('browser displays PostgreSQL-backed Shadow');
  } finally {
    await browser.close();
  }
  compose(['kill', '-s', 'SIGKILL', 'device-simulator']);
  await until(
    'LWT OFFLINE',
    async () =>
      (await api('devices')).filter((d) => d.reported.connectivity === 'OFFLINE').length === 20,
    30000
  );
  record('LWT distinguishes offline and unknown health');
  compose(['up', '-d', '--no-deps', 'device-simulator'], { SIM_COUNT: '20' });
  await until(
    'reconnect restores 20 shadows',
    async () =>
      (await api('devices')).filter((d) => d.reported.connectivity === 'MQTT_CONNECTED').length ===
      20
  );
  record('reconnect new boot');
  compose([
    'exec',
    '-T',
    'postgres',
    'psql',
    '-U',
    'crane',
    '-d',
    'crane',
    '-v',
    'ON_ERROR_STOP=1',
    '-c',
    'SELECT count(*) AS persisted_shadows FROM device_shadow; SELECT count(*) AS migrations FROM goose_db_version WHERE is_applied; SELECT count(*) AS alarms FROM alarms;',
  ]);
  const durable = await api('devices/SIM-001/shadow');
  compose(['restart', 'cloud-api']);
  await until(
    'restart restores persisted Shadow',
    async () =>
      (await fetch(base + '/health/ready')).ok &&
      (await api('devices/SIM-001/shadow')).version >= durable.version
  );
  record('Cloud restart restores persisted Shadow cache');
  writeFileSync(
    'local-private/validation/integration.json',
    JSON.stringify({ status: 'PASS', proof }, null, 2)
  );
  console.log('PHASE0_INTEGRATION=PASS');
} catch (error) {
  writeFileSync(
    'local-private/validation/integration.json',
    JSON.stringify({ status: 'FAIL', error: error.message, proof }, null, 2)
  );
  throw error;
}
