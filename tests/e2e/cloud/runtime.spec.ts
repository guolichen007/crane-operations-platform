import { test, expect, type Page, type WebSocketRoute } from '@playwright/test';
import {
  cloudFleet,
  cloudDevice,
  cloudAlarm,
  viewerLease,
} from '../../../apps/web-console/tests/cloud-fixtures';
const viewerStorageKey = 'crane.cloud.viewer';

async function cloud(page: Page) {
  const fleet = cloudFleet(),
    alarms = [cloudAlarm()];
  const calls: { path: string; method: string }[] = [];
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname,
      method = route.request().method();
    calls.push({ path, method });
    let result: unknown;
    if (method === 'DELETE') {
      await route.fulfill({ status: 204 });
      return;
    }
    if (path === '/api/v1/projects' || path === '/api/v1/assets/devices') result = [];
    else if (path === '/api/v1/devices') result = fleet;
    else if (path === '/api/v1/alarms') result = alarms;
    else if (path.endsWith('/shadow')) result = fleet.find((d) => path.includes(d.deviceId));
    else if (path.endsWith('/live-sessions') || path.endsWith('/renew')) result = viewerLease();
    else {
      await route.fulfill({ status: 404 });
      return;
    }
    await route.fulfill({ json: result, status: path.endsWith('/live-sessions') ? 201 : 200 });
  });
  return { fleet, alarms, calls };
}
test('Cloud Alarm uses API records and Shanghai time, without Demo ACK or invented status', async ({
  page,
}) => {
  const h = await cloud(page);
  await page.goto('/#/alarms');
  await expect(page.getByTestId('cloud-alarm')).toHaveCount(1);
  await expect(page.getByTestId('cloud-alarms')).toContainText('SIM_OVERLOAD');
  await expect(page.getByTestId('cloud-alarms')).toContainText('Cloud 测试负载告警');
  await expect(page.getByTestId('cloud-alarms').locator('time')).toHaveText('2026-10-22 16:49:15');
  await expect(page.getByTestId('cloud-alarms').locator('time')).toHaveAttribute(
    'title',
    cloudAlarm().sentAt
  );
  await expect(page.getByRole('button', { name: '确认告警' })).toHaveCount(0);
  await expect(page.getByText('DEMO MOCK', { exact: true })).toHaveCount(0);
  expect(h.calls.some((r) => r.path === '/api/v1/alarms')).toBe(true);
  h.alarms.length = 0;
  await page.getByRole('button', { name: '刷新告警' }).click();
  await expect(page.getByTestId('cloud-alarm')).toHaveCount(0);
  await expect(page.getByText('无匹配的云端告警记录')).toBeVisible();
});
test('API failure displays error, never substitutes Demo records', async ({ page }) => {
  await cloud(page);
  await page.route('**/api/v1/alarms', (route) => route.fulfill({ status: 503 }));
  await page.goto('/#/alarms');
  await expect(page.getByTestId('cloud-alarms')).toContainText('云端请求失败 (503)');
  await expect(page.getByTestId('cloud-alarm')).toHaveCount(0);
  await expect(page.getByText('DEMO MOCK', { exact: true })).toHaveCount(0);
});
test('Overview reports 20 Cloud devices separately; missing site metadata creates no spatial point', async ({
  page,
}) => {
  const h = await cloud(page);
  h.fleet[0]!.upstreamStatus = 'LIVE';
  await page.goto('/#/overview');
  const summary = page.getByTestId('cloud-runtime');
  await expect(summary.locator('.runtime-metrics b')).toHaveText(['20', '20', '1', '1']);
  await expect(summary).toContainText('未配置时不生成地图点');
  await expect(summary).toContainText('SIM 为模拟设备');
  expect(h.calls.some((r) => r.path === '/api/v1/alarms')).toBe(true);
  expect(h.calls.some((r) => r.path === '/api/v1/devices')).toBe(true);
  await expect(page.locator('.scene-loading')).toBeHidden();
  await expect(page.locator('.scene-error')).toHaveCount(0);
  // Existing demo map remains intact; no Cloud device is injected into any label or pick target.
  await expect(page.locator('.province-summary')).toHaveCount(34);
  await expect(page.locator('.sandbox-stage')).not.toContainText('SIM-');
  const picks = await page.evaluate(() => window.__spatialDiagnostics!().picks);
  expect(picks.every((p) => !p.id.startsWith('SIM-'))).toBe(true);
  await page.screenshot({ path: 'test-results/s35-overview.png' });
});
test('1920x1080 bottom cards clear CloudStatus, with readable SILENT and localized timestamps', async ({
  page,
}) => {
  await cloud(page);
  await page.goto('/#/runtime');
  await expect(page.getByTestId('cloud-device')).toHaveCount(20);
  await expect(page.getByTestId('cloud-device').first()).toContainText('在线 · 静默监测');
  await expect(page.getByTestId('cloud-device').first().locator('time')).toHaveText(
    '2026-10-22 16:49:15'
  );
  await expect(page.getByText(/静默监测：设备保持云端连接/)).toBeVisible();
  await page.locator('.cloud-devices').evaluate((el) => {
    el.scrollTop = el.scrollHeight;
  });
  const bar = await page.locator('.cloud-status').boundingBox();
  for (const id of ['SIM-019', 'SIM-020']) {
    const card = page.getByTestId('cloud-device').filter({ hasText: id });
    const box = await card.boundingBox();
    expect(box).not.toBeNull();
    expect(box!.y).toBeGreaterThanOrEqual(60);
    expect(box!.y + box!.height).toBeLessThanOrEqual(bar!.y);
  }
  await page.screenshot({ path: 'test-results/s35-device-safe-area.png' });
});
test('native WebSocket reconnects without another Viewer, then active stop prevents retry', async ({
  page,
}) => {
  const h = await cloud(page),
    sockets: WebSocketRoute[] = [];
  await page.routeWebSocket('**/api/v1/devices/*/stream', (ws) => {
    sockets.push(ws);
    ws.send(JSON.stringify({ ...cloudDevice(), upstreamStatus: 'LIVE' }));
  });
  await page.goto('/#/devices/SIM-001');
  await expect(page.getByRole('heading', { name: /Last Known State/ })).toBeVisible();
  await page.getByRole('button', { name: '申请 LIVE · 1 Hz', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'LIVE 上游：LIVE', exact: true })).toBeVisible();
  expect(await page.evaluate((key) => sessionStorage.getItem(key), viewerStorageKey)).toBe(
    'viewer-1'
  );
  sockets[0]!.close({ code: 1011, reason: 'test transport loss' });
  await expect(page.locator('h2[data-stream-state]')).toHaveAttribute(
    'data-stream-state',
    'RECONNECTING'
  );
  await expect.poll(() => sockets.length).toBe(2);
  await expect(page.locator('h2[data-stream-state]')).toHaveAttribute('data-stream-state', 'LIVE');
  expect(
    h.calls.filter((r) => r.method === 'POST' && r.path.endsWith('/live-sessions'))
  ).toHaveLength(1);
  await page.getByRole('button', { name: '停止实时查看', exact: true }).click();
  await expect(page.getByRole('heading', { name: /Last Known State/ })).toBeVisible();
  expect(await page.evaluate((key) => sessionStorage.getItem(key), viewerStorageKey)).toBeNull();
  await page.waitForTimeout(2200);
  expect(sockets).toHaveLength(2);
  expect(h.calls.filter((r) => r.method === 'DELETE')).toHaveLength(1);
});
test('pagehide sends keepalive DELETE; reload clears stale id without reopening LIVE', async ({
  page,
}) => {
  const h = await cloud(page);
  await page.routeWebSocket('**/api/v1/devices/*/stream', (ws) =>
    ws.send(JSON.stringify(cloudDevice()))
  );
  await page.goto('/#/devices/SIM-001');
  await page.getByRole('button', { name: '申请 LIVE · 1 Hz', exact: true }).click();
  await expect(page.locator('h2[data-stream-state]')).toHaveAttribute('data-stream-state', 'LIVE');
  await page.evaluate(() => window.dispatchEvent(new Event('pagehide')));
  await expect.poll(() => h.calls.filter((r) => r.method === 'DELETE').length).toBe(1);
  await page.evaluate((key) => sessionStorage.setItem(key, 'stale-after-crash'), viewerStorageKey);
  await page.reload();
  await expect
    .poll(() => h.calls.some((r) => r.path.endsWith('/stale-after-crash') && r.method === 'DELETE'))
    .toBe(true);
  await expect(page.getByRole('heading', { name: /Last Known State/ })).toBeVisible();
  expect(await page.evaluate((key) => sessionStorage.getItem(key), viewerStorageKey)).toBeNull();
  expect(h.calls.filter((r) => r.method === 'POST')).toHaveLength(1);
});
