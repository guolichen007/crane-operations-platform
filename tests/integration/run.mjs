import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, mkdirSync, existsSync } from 'node:fs';
import { resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
const win = process.platform === 'win32';
const go = existsSync('tools/.cache/go/bin/go.exe') ? resolve('tools/.cache/go/bin/go.exe') : 'go';
const docker = win ? 'C:/Program Files/Docker/Docker/resources/bin/docker.exe' : 'docker';
const config = Object.fromEntries(readFileSync('deploy/compose/runtime/.env', 'utf8').trim().split(/\r?\n/).map((line) => { const i = line.indexOf('='); return [line.slice(0, i), line.slice(i + 1)]; }));
const token = config.DEV_API_TOKEN;
const base = process.env.PHASE0_API_URL || 'http://127.0.0.1:8081';
const env = { ...process.env, GOCACHE: resolve('tools/.cache/gobuild'), GOMODCACHE: resolve('tools/.cache/gomod'), GOPATH: resolve('tools/.cache/gopath') };
const testConfig = { LIVE_TTL_SECONDS: '30', LIVE_GRACE_SECONDS: '2', MQTT_WILL_DELAY_SECONDS: '5', SIM_COUNT: '20' };
const composeArgs = ['compose', '--env-file', 'deploy/compose/runtime/.env', '-f', 'deploy/compose/docker-compose.dev.yml'];
function run(cmd, args, extra = {}, capture = false) {
  const result = spawnSync(cmd, args, { encoding: 'utf8', stdio: capture ? 'pipe' : 'inherit', env: { ...env, ...extra } });
  if (result.error) throw result.error;
  assert.equal(result.status, 0, `${cmd} failed${capture ? ': ' + result.stderr : ''}`);
  return result.stdout?.trim();
}
function compose(args, extra = {}, capture = false) { return run(docker, [...composeArgs, ...args], { ...testConfig, ...extra }, capture); }
function db(sql) { return compose(['exec', '-T', 'postgres', 'psql', '-U', 'crane', '-d', 'crane', '-v', 'ON_ERROR_STOP=1', '-At', '-c', sql], {}, true); }
const pause = (ms) => new Promise((done) => setTimeout(done, ms));
async function api(path, method = 'GET', expected = 200) {
  const res = await fetch(base + '/api/v1/' + path, { method, headers: { Authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(5000) });
  assert.equal(res.status, expected, `${method} ${path}`);
  return expected === 204 ? null : res.json();
}
async function until(label, fn, timeout = 45000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) {
    try { const value = await fn(); if (value) { console.log('PASS ' + label); return value; } } catch { /* dependency warming up */ }
    await pause(500);
  }
  throw Error(`Timed out: ${label}`);
}
const proof = [];
const record = (check) => { proof.push({ check, status: 'PASS', at: new Date().toISOString() }); console.log('PASS ' + check); };
const root = 'local-private/validation/t05';
mkdirSync(root, { recursive: true });
const shadow = () => api('devices/SIM-001/shadow');
const simulated = async () => (await api('devices')).filter((d) => d.deviceId.startsWith('SIM-'));
const ready = () => until('cloud readiness', async () => (await fetch(base + '/health/ready')).ok);
async function browserCheck() {
  compose(['exec', '-T', 'web-console', 'nginx', '-s', 'reload']);
  const { chromium } = await import('@playwright/test');
  const browser = await chromium.launch(win ? { channel: 'chrome' } : { headless: true });
  try {
    const page = await browser.newPage();
    await page.goto((process.env.PHASE0_WEB_URL || `http://127.0.0.1:${config.WEB_PORT || 18080}`) + '/#/devices');
    await page.getByLabel('本地开发凭据', { exact: true }).fill(token);
    await page.getByRole('button', { name: '连接云端', exact: true }).click();
    await page.getByTestId('cloud-device').first().waitFor();
    const fleetCards = page.getByTestId('cloud-device').filter({ hasText: /SIM-\d{3}/ });
    assert.equal(await fleetCards.count(), 20);
    assert.equal(await page.getByTestId('cloud-device').count(), (await api('devices')).length);
    await fleetCards.first().click();
    await page.getByTestId('device-shadow').waitFor();
    assert.match(await page.getByTestId('device-shadow').textContent(), /Last Known State/);
    await page.getByRole('button', { name: '申请 LIVE · 1 Hz', exact: true }).click();
    await page.getByRole('heading', { name: 'LIVE 上游：LIVE', exact: true }).waitFor();
    await page.screenshot({ path: root + '/cloud-shadow.png', fullPage: true });
    await page.getByRole('button', { name: '停止实时查看', exact: true }).click();
    record('browser PostgreSQL Shadow and application-ACK LIVE status');
  } finally { await browser.close(); }
}
try {
  await ready();
  assert.equal((await fetch(base + '/api/v1/devices')).status, 401);
  record('unauthorized API rejected');
  assert.equal(createHash('sha256').update(readFileSync('deploy/postgres/migrations/00001_baseline.sql')).digest('hex'), 'b6f6883d8ece708e40763882e651d0072ce0f5df2be0f66dc49ca72a2586e7c0');
  // Execute the same Goose migration code and repository against isolated databases.
  const binary = resolve('tools/.cache/session.test');
  run(go, ['test', '-c', '-o', binary, './services/cloud-api/internal/session'], { GOOS: 'linux', GOARCH: 'amd64', CGO_ENABLED: '0' });
  compose(['run', '--rm', '--no-deps', '--entrypoint', 'sh', '-v', `${binary}:/tmp/session.test:ro`, 'cloud-api', '-c', 'T05_TEST_DATABASE_URL="$DATABASE_URL" /tmp/session.test -test.v']);
  record('empty DB and Phase0 DB upgrade; original migration immutable');
  record('five viewer reference, authorization, expiry and durable generation repository');
  compose(['up', '-d', '--force-recreate', '--no-deps', 'cloud-api']);
  await ready();
  const restart = Date.now();
  compose(['up', '-d', '--force-recreate', '--no-deps', 'device-simulator']);
  await until('20 independent online device identities', async () => (await simulated()).filter((d) => d.presence.connectivity === 'MQTT_CONNECTED' && Date.parse(d.updatedAt) >= restart).length === 20);
  assert.equal(db('SELECT max(version_id) FROM goose_db_version'), '2');
  record('20 simulated devices Edge → MQTT5 TLS → PostgreSQL typed Shadow');
  await browserCheck();
  await until('browser lease closes with grace', async () => (await shadow()).upstreamStatus === 'SILENT');
  const beforeStarts = Number(db("SELECT count(*) FROM device_commands WHERE device_id='SIM-001' AND kind='WATCH_START'"));
  const sessions = [];
  for (let i = 0; i < 5; i++) sessions.push(await api('devices/SIM-001/live-sessions', 'POST', 201));
  assert.equal(sessions[0].upstreamStatus, 'STARTING');
  const live = await until('five viewers confirmed LIVE', async () => { const d = await shadow(); return d.upstreamStatus === 'LIVE' && d.reported.operatingState === 'LIVE' && d; });
  assert.equal(db("SELECT count(*) FROM device_live_state WHERE device_id='SIM-001'"), '1');
  assert.equal(Number(db("SELECT count(*) FROM device_commands WHERE device_id='SIM-001' AND kind='WATCH_START'")) - beforeStarts, 1);
  assert.equal(db("SELECT count(*) FROM live_sessions WHERE device_id='SIM-001' AND expires_at>now()"), '5');
  assert.match(await (await fetch(base + '/metrics')).text(), /device_live_upstream_active 1\b/);
  await until('1Hz LIVE advances', async () => (await shadow()).version > live.version);
  record('five viewers share exactly one device upstream and one START generation');
  for (const s of sessions) await api(`live-sessions/${s.sessionId}/renew`, 'POST');
  const oldGeneration = Number(db("SELECT generation FROM device_live_state WHERE device_id='SIM-001'"));
  compose(['restart', 'cloud-api']);
  await ready();
  await until('Cloud restart restores unexpired desired LIVE', async () => (await shadow()).upstreamStatus === 'LIVE');
  assert.equal(db("SELECT count(*) FROM live_sessions WHERE device_id='SIM-001' AND expires_at>now()"), '5');
  assert.ok(Number(db("SELECT generation FROM device_live_state WHERE device_id='SIM-001'")) > oldGeneration);
  record('Cloud restart restores persisted Viewer Lease and reconciles desired generation');
  for (const s of sessions) await api(`live-sessions/${s.sessionId}/renew`, 'POST');
  const oldBoot = (await shadow()).presence.bootSeq;
  compose(['restart', 'device-simulator']);
  await until('Edge restart BIRTH reconciles active Viewer Leases', async () => { const d = await shadow(); return d.presence.bootSeq > oldBoot && d.upstreamStatus === 'LIVE' && d.reported.operatingState === 'LIVE'; });
  record('Edge restart persists bootSeq and Cloud automatically reconciles desired LIVE');
  for (const s of sessions) await api(`live-sessions/${s.sessionId}/renew`, 'POST');
  const beforeReconnect = await shadow();
  const beforeReconnectGeneration = Number(db("SELECT generation FROM device_live_state WHERE device_id='SIM-001'"));
  compose(['restart', 'mosquitto']);
  await ready();
  await until('MQTT-only reconnect reconciles LIVE without a new Agent boot', async () => {
    const d = await shadow();
    return d.presence.bootSeq === beforeReconnect.presence.bootSeq && d.presence.sequence > beforeReconnect.presence.sequence && d.upstreamStatus === 'LIVE' && Number(db("SELECT generation FROM device_live_state WHERE device_id='SIM-001'")) > beforeReconnectGeneration;
  });
  assert.match(await (await fetch(base + '/metrics')).text(), /mqtt_reconnects_total [1-9]\d*/);
  record('MQTT reconnect preserves bootSeq and BIRTH automatically reconciles desired LIVE');
  for (const s of sessions) await api(`live-sessions/${s.sessionId}`, 'DELETE', 204);
  await until('last viewer grace returns SILENT', async () => (await shadow()).upstreamStatus === 'SILENT' && (await shadow()).reported.operatingState === 'SILENT', 20000);
  record('last viewer close and grace safely return SILENT');
  // Retain Phase0's TLS ACL and duplicate/out-of-order checks with typed envelopes.
  const current = await shadow();
  const m = { schemaVersion: 1, messageId: 't05-alarm-' + Date.now(), tenantId: current.tenantId, projectId: current.projectId, deviceId: current.deviceId, bootId: current.presence.bootId, bootSeq: current.presence.bootSeq, sequence: current.presence.sequence + 1, sentAt: new Date().toISOString(), kind: 'ALARM', correlationId: '', payload: { code: 'SIMULATED_OVERLOAD', text: 'Anonymous integration alarm' } };
  const fixture = root + '/mqtt-message.json';
  writeFileSync(fixture, JSON.stringify(m));
  run(go, ['run', './tools/diagnostics/mqtt-probe', '--message', fixture]);
  await until('alarm deduplication', async () => (await api('alarms')).filter((a) => a.messageId === m.messageId).length === 1);
  m.kind = 'SNAPSHOT'; m.sequence = Math.max(1, current.presence.sequence - 1); m.payload = { edgeHealth: 'EDGE_HEALTHY', controllerHealth: 'CONTROLLER_HEALTHY', sensorHealth: 'HEALTHY', reported: { ...current.reported, position: { x: 999, y: 999, z: 999 } } };
  writeFileSync(fixture, JSON.stringify(m));
  run(go, ['run', './tools/diagnostics/mqtt-probe', '--message', fixture]);
  await pause(1000); assert.notEqual((await shadow()).reported.position.x, 999);
  record('TLS ACL denies cross-device/control; silent alarm dedup and out-of-order rejection');
  // A real Agent must retain its queue after broker PUBACK while Cloud is unavailable.
  compose(['stop', 'cloud-api']);
  compose(['up', '-d', '--force-recreate', '--no-deps', 'device-simulator'], { SIM_SCENARIO: 'alarm-storm' });
  await pause(7000);
  const queued = JSON.parse(compose(['exec', '-T', 'device-simulator', 'cat', '/spool/SIM-001.json'], {}, true));
  assert.ok(queued.length > 0);
  const pendingID = queued[0].messageId;
  const metrics = compose(['exec', '-T', 'device-simulator', 'wget', '-qO-', 'http://127.0.0.1:8083/metrics'], {}, true);
  assert.match(metrics, /critical_spool_depth [1-9]\d*/);
  assert.match(metrics, /critical_spool_retries_total [1-9]\d*/);
  record('broker PUBACK while Cloud down leaves real Agent critical spool intact');
  compose(['start', 'cloud-api']); await ready();
  await until('Cloud commit and application ACK drain old critical head', async () => {
    const q = JSON.parse(compose(['exec', '-T', 'device-simulator', 'cat', '/spool/SIM-001.json'], {}, true));
    return !q.some((event) => event.messageId === pendingID) && db(`SELECT count(*) FROM alarms WHERE message_id='${pendingID}'`) === '1';
  });
  record('Cloud recovery commits critical event and application ACK removes durable spool');
  compose(['up', '-d', '--force-recreate', '--no-deps', 'device-simulator']);
  await until('normal fleet restores', async () => (await simulated()).filter((d) => d.presence.connectivity === 'MQTT_CONNECTED').length === 20);
  // Direct socket faults verify the broker's actual MQTT5 semantics, not log guesses.
  run(go, ['test', ...(process.env.CI ? ['-race'] : []), '-count=1', '-v', './tests/integration/mqtt'], { T05_INTEGRATION: '1', T05_PROJECT_ROOT: resolve('.'), T05_API_URL: base, T05_API_TOKEN: token });
  record('short disconnect suppresses Will; long disconnect emits delayed offline');
  record('lost event application ACK retries same ID, stores once and ACKs again');
  record('MQTT Message Expiry prevents queued old WATCH delivery');
  const noAck = await api('devices/TEST-001/live-sessions', 'POST', 201);
  assert.equal(noAck.upstreamStatus, 'STARTING');
  await until('command published without an Edge ACK', async () => db("SELECT count(*) FROM device_commands WHERE device_id='TEST-001' AND last_sent_at IS NOT NULL AND acked_at IS NULL") !== '0');
  const deadline = Date.now() + 11500;
  while (Date.now() < deadline) { assert.notEqual((await api('devices/TEST-001/shadow')).upstreamStatus, 'LIVE'); await pause(500); }
  assert.equal(db("SELECT count(*) FROM device_commands WHERE device_id='TEST-001' AND status='APPLIED'"), '0');
  assert.ok(Number(db("SELECT count(*) FROM device_commands WHERE device_id='TEST-001' AND status='EXPIRED'")) >= 1);
  await api(`live-sessions/${noAck.sessionId}`, 'DELETE', 204);
  record('201/PUBACK without WATCH application ACK never promotes LIVE; timeout persists EXPIRED');
  const previous = await shadow();
  compose(['stop', 'device-simulator']);
  await until('graceful DEATH', async () => (await simulated()).filter((d) => d.presence.connectivity === 'OFFLINE' && d.presence.disconnectReason === 'GRACEFUL').length === 20);
  assert.equal((await shadow()).reported.mode, previous.reported.mode);
  assert.deepEqual((await shadow()).reported.position, previous.reported.position);
  assert.equal((await shadow()).reported.load, previous.reported.load);
  record('SIGTERM emits graceful DEATH and preserves last AUTO/XYZ/load');
  compose(['up', '-d', '--no-deps', 'device-simulator']);
  await until('20 online after graceful restart', async () => (await simulated()).filter((d) => d.presence.connectivity === 'MQTT_CONNECTED').length === 20);
  compose(['kill', '-s', 'SIGKILL', 'device-simulator']);
  await until('20 unexpected LWT offline', async () => (await simulated()).filter((d) => d.presence.connectivity === 'OFFLINE' && d.presence.disconnectReason === 'UNEXPECTED').length === 20, 20000);
  record('20-device abnormal death uses delayed LWT, distinct from graceful shutdown');
  compose(['up', '-d', '--no-deps', 'device-simulator']);
  await until('20 online stable after fault recovery', async () => (await simulated()).filter((d) => d.presence.connectivity === 'MQTT_CONNECTED').length === 20);
  // Preserve the unrenewed browser TTL counterexample with short, test-only limits.
  compose(['up', '-d', '--force-recreate', '--no-deps', 'cloud-api'], { LIVE_TTL_SECONDS: '5' }); await ready();
  await api('devices/SIM-001/live-sessions', 'POST', 201);
  await until('unrenewed lease LIVE', async () => (await shadow()).upstreamStatus === 'LIVE');
  await until('unrenewed lease expires to SILENT', async () => (await shadow()).upstreamStatus === 'SILENT' && (await shadow()).reported.operatingState === 'SILENT', 20000);
  record('unrenewed Viewer TTL expires and stops upstream after grace');
  writeFileSync(root + '/integration.json', JSON.stringify({ status: 'PASS', proof }, null, 2));
  console.log(`T0_5_INTEGRATION=PASS checks=${proof.length}`);
} catch (error) {
  writeFileSync(root + '/integration.json', JSON.stringify({ status: 'FAIL', error: error.message, proof }, null, 2));
  throw error;
}
