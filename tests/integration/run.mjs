import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, mkdirSync, existsSync, statSync } from 'node:fs';
import { resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { assetServices, assertAssetServiceScope } from '../../tools/asset-runtime.mjs';
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
const root = 'local-private/validation/phase1-a0-hardening';
mkdirSync(root, { recursive: true });
const shadow = () => api('devices/SIM-001/shadow');
const simulated = async () => (await api('devices')).filter((d) => d.deviceId.startsWith('SIM-'));
const ready = () => until('cloud readiness', async () => (await fetch(base + '/health/ready')).ok);
async function browserCheck() {
  compose(['exec', '-T', 'web-console', 'nginx', '-s', 'reload']);
  const { chromium, expect } = await import('@playwright/test');
  const browser = await chromium.launch(win ? { channel: 'chrome' } : { headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 1920, height: 1080 } });
    await page.goto((process.env.PHASE0_WEB_URL || `http://127.0.0.1:${config.WEB_PORT || 18080}`) + '/#/runtime');
    await page.getByLabel('本地开发凭据', { exact: true }).fill(token);
    await page.getByRole('button', { name: '连接云端', exact: true }).click();
    await page.getByTestId('cloud-device').first().waitFor();
    const fleetCards = page.getByTestId('cloud-device').filter({ hasText: /SIM-\d{3}/ });
    assert.equal(await fleetCards.count(), 20);
    assert.equal(await page.getByTestId('cloud-device').count(), (await api('devices')).length);
    await expect(fleetCards.first()).toContainText('在线 · 静默监测');
    await page.locator('.cloud-devices').evaluate((el) => { el.scrollTop = el.scrollHeight; });
    const bar = await page.locator('.cloud-status').boundingBox();
    for (const id of ['SIM-019', 'SIM-020']) {
      const box = await fleetCards.filter({ hasText: id }).boundingBox();
      assert.ok(box && bar && box.y >= 60 && box.y + box.height <= bar.y, `${id} clears CloudStatus`);
    }
    await page.getByRole('link', { name: '态势沙盘', exact: true }).click();
    const runtime = page.getByTestId('cloud-runtime');
    await expect(runtime.locator('.runtime-metrics b').first()).toHaveText(String((await api('devices')).length));
    await expect(runtime).toContainText('未配置时不生成地图点');
    await expect(runtime).toContainText('SIM 为模拟设备');
    record('S3.5 real Cloud runtime summary and 1920x1080 grid safe area');
    await page.getByRole('link', { name: '告警中心', exact: true }).click();
    const alarms = await api('alarms');
    await expect(page.getByTestId('cloud-alarm')).toHaveCount(alarms.length);
    if (alarms.length) await expect(page.getByTestId('cloud-alarm').first()).toContainText(alarms[0].payload.code);
    await expect(page.getByRole('button', { name: '确认告警' })).toHaveCount(0);
    record('S3.5 Cloud Alarm page matches actual Alarm API without Demo ACK');
    await page.getByRole('link', { name: '查看云端设备 →', exact: true }).click();
    await page.getByTestId('cloud-device').filter({ hasText: 'SIM-001' }).click();
    await page.getByTestId('device-shadow').waitFor();
    assert.match(await page.getByTestId('device-shadow').textContent(), /Last Known State/);
    await page.getByRole('button', { name: '申请 LIVE · 1 Hz', exact: true }).click();
    await page.getByRole('heading', { name: 'LIVE 上游：LIVE', exact: true }).waitFor();
    const versionBefore = await page.getByTestId('device-shadow').getAttribute('data-shadow-version');
    await expect(page.getByTestId('device-shadow')).not.toHaveAttribute('data-shadow-version', versionBefore);
    await expect(page.getByTestId('device-shadow').locator('time').first()).toHaveText(/\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}/);
    await page.screenshot({ path: root + '/cloud-shadow.png', fullPage: true });
    await page.getByRole('button', { name: '停止实时查看', exact: true }).click();
    record('browser PostgreSQL Shadow, 1Hz updates and application-ACK LIVE status');
    await page.getByRole('button', { name: '申请 LIVE · 1 Hz', exact: true }).click();
    await page.getByRole('heading', { name: 'LIVE 上游：LIVE', exact: true }).waitFor();
    const leaseId = await page.evaluate(() => sessionStorage.getItem('crane.cloud.viewer'));
    assert.ok(leaseId);
    await page.reload();
    await page.getByRole('heading', { name: /Last Known State/ }).waitFor();
    await until('reload closes actual persisted Viewer Lease', () => Number(db(`SELECT count(*) FROM live_sessions WHERE session_id='${leaseId}'`)) === 0);
    assert.equal(await page.evaluate(() => sessionStorage.getItem('crane.cloud.viewer')), null);
    record('S3.5 refresh cleans persisted Viewer and returns Last Known State without automatic LIVE');
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
  const inventoryBinary = resolve('tools/.cache/inventory.test');
  run(go, ['test', '-c', '-o', inventoryBinary, './internal/inventory'], { GOOS: 'linux', GOARCH: 'amd64', CGO_ENABLED: '0' });
  compose(['run', '--rm', '--no-deps', '--entrypoint', 'sh', '-v', `${inventoryBinary}:/tmp/inventory.test:ro`, '-v', `${resolve('deploy/inventory/inventory.example.yaml')}:/fixtures/example.yaml:ro`, 'cloud-api', '-c', 'A0_TEST_DATABASE_URL="$DATABASE_URL" A0_INVENTORY_EXAMPLE=/fixtures/example.yaml /tmp/inventory.test -test.v']);
  record('A0 empty/v2 migration, idempotent atomic import, provenance and guarded legacy reset');
  const provenanceBinary = resolve('tools/.cache/provenance.test');
  run(go, ['test', '-c', '-o', provenanceBinary, './services/cloud-api/internal/app'], { GOOS: 'linux', GOARCH: 'amd64', CGO_ENABLED: '0' });
  compose(['run', '--rm', '--no-deps', '--entrypoint', 'sh', '-v', `${provenanceBinary}:/tmp/provenance.test:ro`, '-v', `${resolve('deploy/inventory/inventory.example.yaml')}:/fixtures/example.yaml:ro`, 'cloud-api', '-c', 'A0_TEST_DATABASE_URL="$DATABASE_URL" A0_INVENTORY_EXAMPLE=/fixtures/example.yaml /tmp/provenance.test -test.v']);
  record('Hardening real Cloud startup rejects missing first/last runtime and project/source/asset/tenant mismatch before MQTT or HTTP');
  record('Hardening correct Inventory -> Fleet passes; Store rejects unregistered SIM/EDGE first packet and retains SIM-001..020 plus TEST-001');
  compose(['up', '-d', '--force-recreate', '--no-deps', 'cloud-api']);
  await ready();
  const restart = Date.now();
  compose(['up', '-d', '--force-recreate', '--no-deps', 'device-simulator']);
  await until('20 independent online device identities', async () => (await simulated()).filter((d) => d.presence.connectivity === 'MQTT_CONNECTED' && Date.parse(d.updatedAt) >= restart).length === 20);
  assert.equal(db('SELECT max(version_id) FROM goose_db_version'), '4');
  assert.equal(db('SELECT count(*) FROM project_memberships'), '0');
  record('A1 migration applied without granting projects to legacy development identities');
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
  await assetModeCheck();
  writeFileSync(root + '/integration.json', JSON.stringify({ status: 'PASS', proof }, null, 2));
  console.log(`PHASE1_A0_HARDENING_INTEGRATION=PASS checks=${proof.length}`);
} catch (error) {
  writeFileSync(root + '/integration.json', JSON.stringify({ status: 'FAIL', error: error.message, proof }, null, 2));
  throw error;
}

async function assetModeCheck() {
  const name = `crane_a0_${Date.now()}`;
  assert.match(name, /^crane_a0_[0-9]+$/);
  db(`CREATE DATABASE ${name}`);
  const assetEnv = { POSTGRES_DATABASE: name, LIVE_TTL_SECONDS: '30', LIVE_GRACE_SECONDS: '2', MQTT_WILL_DELAY_SECONDS: '5' };
  const assetCompose = (args, capture = false) => run(docker, [...composeArgs, '-f', 'deploy/compose/docker-compose.inventory.yml', ...args], assetEnv, capture);
  const assetDB = sql => assetCompose(['exec', '-T', 'postgres', 'psql', '-U', 'crane', '-d', name, '-v', 'ON_ERROR_STOP=1', '-At', '-c', sql], true);
  let browser;
  try {
    compose(['stop', 'device-simulator', 'cloud-api', 'mosquitto']);
    // Start from a fresh web/gateway fixture, as the old --force-recreate path
    // did: nginx must not retain the old Cloud container's upstream address.
    // Exercise actual asset-up so an unqualified `up` is observable.
    compose(['rm', '--stop', '--force', 'web-console', 'remote-gateway']);
    const resolved = JSON.parse(assetCompose(['config', '--format', 'json'], true));
    assert.deepEqual(assertAssetServiceScope(resolved), [...assetServices].sort());
    run(process.execPath, ['tools/task.mjs', 'asset-up'], { ...assetEnv, INVENTORY_FILE: resolve('deploy/inventory/inventory.example.yaml') });
    assert.deepEqual(assetCompose(['ps', '--services', '--status', 'running'], true).split(/\r?\n/).filter(Boolean).sort(), [...assetServices].sort());
    record('Hardening actual asset-up resolved dependency set and running service set are exactly the five permitted services; no Remote Gateway or PointCloud');
    if (!win) {
      assert.equal(statSync('deploy/compose/runtime').mode & 0o777, 0o700);
      for (const name of ['.env', 'inventory.json', 'fleet.json', 'acl']) assert.equal(statSync(`deploy/compose/runtime/${name}`).mode & 0o777, 0o600, name);
      record('Hardening Unix actual generated runtime directory=0700; inventory/fleet/ACL/env=0600');
    }
    await ready();
    const expected = JSON.parse(readFileSync('deploy/compose/runtime/fleet.json', 'utf8')).devices.map(d => d.runtimeDeviceId).sort();
    await until('A0 exact10 online manifest identities', async () => {
      const devices = await api('devices');
      return JSON.stringify(devices.map(d => d.deviceId).sort()) === JSON.stringify(expected) && devices.every(d => d.presence.connectivity === 'MQTT_CONNECTED');
    });
    assert.equal(expected.length, 10);
    assert.equal(assetDB('SELECT count(*) FROM projects'), '9');
    assert.equal(assetDB('SELECT count(*) FROM asset_devices'), '10');
    assert.equal(assetDB("SELECT count(*) FROM devices WHERE source_mode='SIMULATION'"), '10');
    assert.equal(assetDB("SELECT count(*) FROM devices WHERE source_mode='REAL'"), '0');
    const projects = await api('projects'), assets = await api('assets/devices');
    assert.equal(projects.length, 9); assert.equal(assets.length, 10);
    assert.equal(projects.filter(p => p.lifecycle === 'PLANNED').length, 5);
    assert.equal(projects.filter(p => p.lifecycle === 'PRE_DEVELOPMENT').length, 1);
    assert.equal(projects.filter(p => p.lifecycle === 'COMPLETED' && p.assetDeviceCount === 0 && p.runtimeDeviceCount === 0).length, 3);
    assert.ok(assets.every(a => a.runtimeSource === 'SIMULATION' && a.connectivity === 'MQTT_CONNECTED'));
    record('A0 real PostgreSQL/API: 9 projects,10 assets,10 simulation,0 real; completed projects have no fake devices');
    run(go, ['run', './tools/diagnostics/mqtt-probe', '--device', 'SIM-P001-01', '--project', 'P001', '--forbidden-project', 'P004', '--forbidden-device', 'SIM-P001-01']);
    record('A0 TLS ACL permits own P001 scope and rejects same identity publishing into P004');
    const lease = await api('devices/SIM-P004-02/live-sessions', 'POST', 201);
    await until('A0 P004 WATCH and LIVE use descriptor identity', async () => (await api('devices/SIM-P004-02/shadow')).upstreamStatus === 'LIVE');
    await api(`live-sessions/${lease.sessionId}`, 'DELETE', 204);
    record('A0 cross-project descriptor controls correct WATCH/LIVE without Project-A fallback');
    const { chromium, expect } = await import('@playwright/test');
    browser = await chromium.launch(win ? { channel: 'chrome' } : { headless: true });
    const page = await browser.newPage({ viewport: { width: 1920, height: 1080 } });
    await page.goto((process.env.PHASE0_WEB_URL || `http://127.0.0.1:${config.WEB_PORT || 18080}`) + '/#/overview');
    await page.getByLabel('本地开发凭据', { exact: true }).fill(token);
    await page.getByRole('button', { name: '连接云端', exact: true }).click();
    await expect(page.getByTestId('inventory-summary').locator('strong')).toHaveText(['9','6','3','10','10','0']);
    await page.getByRole('link', { name: '项目中心', exact: true }).click();
    await expect(page.getByTestId('inventory-project')).toHaveCount(9);
    const completed = page.getByTestId('inventory-project').filter({ hasText: '已完成' });
    await expect(completed).toHaveCount(3);
    for (const card of await completed.all()) { await expect(card).toContainText('待接入Cloud'); await expect(card).not.toContainText('OFFLINE'); }
    await page.getByRole('link', { name: '设备中心', exact: true }).click();
    await expect(page.getByTestId('inventory-asset')).toHaveCount(10);
    await expect(page.getByTestId('inventory-asset').first()).toContainText('规划模拟 · 模拟在线');
    await page.screenshot({ path: root + '/asset-mode.png', fullPage: true });
    record('A0 real browser inventory summary, planned simulation labels and completed-awaiting semantics');
  } finally {
    await browser?.close();
    assetCompose(['stop', 'cloud-api', 'device-simulator']);
    db(`DROP DATABASE ${name} WITH (FORCE)`);
    // Restore retained legacy fixture ACL for the next CI run; do not delete any source DB.
    run(go, ['run', './tools/fleet', '--out', 'deploy/compose/runtime/acl']);
  }
}
