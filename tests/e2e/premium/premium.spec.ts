import { test, expect, type Page } from '@playwright/test';
import { readFileSync, writeFileSync } from 'node:fs';
import { cloudDevice, cloudAlarm } from '../../../apps/web-console/tests/cloud-fixtures';
const example = JSON.parse(readFileSync('deploy/inventory/inventory.example.yaml', 'utf8'));
const session = {
  userId: 'synthetic-viewer',
  displayName: '示例观察员',
  projects: [{ projectId: 'P001', projectName: 'Project-A', role: 'viewer' }],
  csrfToken: 'synthetic-csrf-test-only',
  expiresAt: '2099-01-01T00:00:00Z',
  idleExpiresAt: '2099-01-01T00:00:00Z',
};
async function fixture(page: Page, authenticated = true) {
  await page.route('**/auth/session', (r) =>
    r.fulfill({ status: authenticated ? 200 : 401, json: authenticated ? session : {} })
  );
  const projects = example.projects.map((p: { id: string }, i: number) => ({
    ...p,
    adminCode: i === 0 ? 110000 : null,
    assetDeviceCount: example.assetDevices.filter(
      (a: { projectId: string }) => a.projectId === p.id
    ).length,
    simulatedCount: 0,
    runtimeDeviceCount: 0,
    realConnectedCount: 0,
  }));
  const devices = [cloudDevice('SIM-P001-01')];
  const assets = example.assetDevices.map((a: object) => ({
    ...a,
    runtimeSource: 'SIMULATION',
    runtimeDeviceId: 'SIM-P001-01',
    connectivity: 'MQTT_CONNECTED',
  }));
  await page.route('**/api/v1/**', (r) => {
    const path = new URL(r.request().url()).pathname;
    return r.fulfill({
      json:
        path === '/api/v1/projects'
          ? projects
          : path === '/api/v1/assets/devices'
            ? assets
            : path === '/api/v1/devices'
              ? devices
              : path === '/api/v1/alarms'
                ? [cloudAlarm()]
                : devices[0],
    });
  });
}
async function screenshot(page: Page, name: string) {
  await page.screenshot({
    path: test.info().outputPath(name + '.png'),
    animations: 'disabled',
    maskColor: '#091725',
    mask: [page.locator('.command-actions time')],
  });
  await test
    .info()
    .attach(name, { path: test.info().outputPath(name + '.png'), contentType: 'image/png' });
}
test('首次资产指标无需动画帧即可显示真实 API 数值', async ({ page }) => {
  await page.addInitScript(() => {
    window.requestAnimationFrame = () => 0;
  });
  await fixture(page);
  await page.goto('/#/overview');
  await expect(page.getByTestId('inventory-summary').locator('strong')).toHaveText([
    '9',
    '6',
    '3',
    '10',
    '10',
    '0',
  ]);
});
test('告警日期筛选使用上海日期并与显示一致', async ({ page }) => {
  await fixture(page);
  await page.route('**/api/v1/alarms', (route) =>
    route.fulfill({ json: [{ ...cloudAlarm(), sentAt: '2026-10-21T18:00:00Z' }] })
  );
  await page.goto('/#/alarms');
  await page.getByLabel('告警日期', { exact: true }).fill('2026-10-22');
  await expect(page.getByTestId('cloud-alarm')).toHaveCount(1);
  await expect(page.getByTestId('cloud-alarm').locator('time')).toContainText(
    '2026-10-22 02:00:00'
  );
  await page.getByLabel('告警日期', { exact: true }).fill('2026-10-21');
  await expect(page.getByTestId('cloud-alarm')).toHaveCount(0);
});
for (const width of [1920, 1366]) {
  test('未认证封面与无业务请求 ' + width, async ({ page }) => {
    await page.setViewportSize({ width, height: width === 1920 ? 1080 : 768 });
    let businessRequests = 0;
    page.on('request', (r) => {
      if (r.url().includes('/api/v1/')) businessRequests++;
    });
    await fixture(page, false);
    await page.goto('/#/devices');
    await expect(page.getByRole('link', { name: '账号登录' })).toHaveAttribute(
      'href',
      '/auth/login'
    );
    await expect(page.getByTestId('app-shell')).toHaveCount(0);
    await expect(page.getByRole('navigation', { name: '业务导航' })).toHaveCount(0);
    await page.waitForTimeout(1500);
    expect(businessRequests).toBe(0);
    await screenshot(page, 'login-' + width);
  });
  test('认证总览与只读权限视觉 ' + width, async ({ page }) => {
    await page.setViewportSize({ width, height: width === 1920 ? 1080 : 768 });
    await fixture(page);
    await page.goto('/#/overview');
    await expect(page.getByTestId('viewer-only')).toHaveText('只读观察');
    await page.waitForFunction(
      () =>
        typeof window.__spatialDiagnostics === 'function' &&
        window.__spatialDiagnostics().adcode === 100000
    );
    await expect(page.locator('.scene-loading')).toBeHidden();
    await expect(page.locator('.side-rail nav a')).toHaveCount(4);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width);
    await screenshot(page, 'overview-viewer-' + width);
    await page.goto('/#/alarms');
    await expect(page.getByTestId('cloud-alarm')).toHaveCount(1);
    await expect(page.getByRole('button', { name: '确认告警' })).toHaveCount(0);
    await screenshot(page, 'alarms-' + width);
  });
}
test('查询中不挂载、会话过期即卸载且 5xx 可重试', async ({ page }) => {
  await fixture(page);
  await page.route('**/auth/session', async (r) => {
    await new Promise((resolve) => setTimeout(resolve, 700));
    await r.fulfill({ json: session });
  });
  await page.goto('/');
  await expect(page.getByTestId('brand-login-cover')).toHaveAttribute(
    'data-auth-state',
    'AUTH_LOADING'
  );
  await expect(page.getByTestId('app-shell')).toHaveCount(0);
  await expect(page.getByTestId('app-shell')).toBeVisible();
  await page.route('**/api/v1/projects', (r) => r.fulfill({ status: 401, json: {} }));
  await page.goto('/#/projects');
  await expect(page.getByTestId('app-shell')).toHaveCount(0);
  await expect(page.getByRole('link', { name: '账号登录' })).toBeVisible();
  await page.route('**/auth/session', (r) => r.fulfill({ status: 503, json: {} }));
  await page.reload();
  await expect(page.getByTestId('brand-login-cover')).toHaveAttribute(
    'data-auth-state',
    'AUTH_ERROR'
  );
  await expect(page.getByTestId('app-shell')).toHaveCount(0);
  await page.route('**/auth/session', (r) => r.fulfill({ status: 401, json: {} }));
  await page.getByRole('button', { name: '重试连接' }).click();
  await expect(page.getByRole('link', { name: '账号登录' })).toBeVisible();
});
test('reduced-motion / WebGL fallback / 无业务壳层闪现', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.addInitScript(() => {
    const original = HTMLCanvasElement.prototype.getContext;
    HTMLCanvasElement.prototype.getContext = function (
      this: HTMLCanvasElement,
      kind: string,
      ...args: unknown[]
    ) {
      if (kind.includes('webgl')) return null;
      return original.apply(this, [kind, ...args] as Parameters<typeof original>);
    } as typeof original;
  });
  await fixture(page);
  await page.goto('/#/overview');
  await expect(page.getByTestId('webgl-fallback')).toBeVisible();
  await page.goto('/#/devices/SIM-P001-01');
  await expect(page.getByTestId('twin-fallback')).toBeVisible();
  await expect(page.getByTestId('viewer-only')).toBeVisible();
  await screenshot(page, 'fallback');
});
test('路由往返释放 canvas 且孪生 draw calls / triangles 受预算限制', async ({ page }) => {
  await fixture(page);
  await page.goto('/#/devices/SIM-P001-01');
  await expect(page.locator('.twin-canvas canvas')).toHaveCount(1);
  await page.waitForTimeout(1200);
  const stats = await page.evaluate(() =>
    (
      window as unknown as { __twinDiagnostics: () => { drawCalls: number; triangles: number } }
    ).__twinDiagnostics()
  );
  expect(stats.drawCalls).toBeLessThan(350);
  expect(stats.drawCalls).toBeGreaterThan(20);
  expect(stats.triangles).toBeLessThan(700000);
  expect(stats.triangles).toBeGreaterThan(200);
  writeFileSync(test.info().outputPath('twin-budget.json'), JSON.stringify(stats, null, 2));
  await screenshot(page, 'device-twin');
  for (let i = 0; i < 3; i++) {
    await page.goto('/#/projects');
    await expect(page.locator('.twin-canvas canvas,.map-canvas canvas')).toHaveCount(0);
    await page.goto('/#/devices/SIM-P001-01');
    await expect(page.locator('.twin-canvas canvas')).toHaveCount(1);
  }
});
test('20 次行政区下钻往返不持续增长 GPU 资源', async ({ page }) => {
  test.setTimeout(240000);
  // Resource retention is resolution-independent; exercise the supported reduced viewport.
  // Native 1920x1080 screenshots and focused-scene budgets have separate cases above.
  await page.setViewportSize({ width: 1366, height: 768 });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await fixture(page);
  await page.goto('/#/overview');
  await expect(page.getByRole('combobox', { name: '行政区下钻' })).toContainText('北京市');
  await expect(page.locator('.scene-loading')).toBeHidden();
  const counts: number[] = [];
  for (let i = 0; i < 20; i++) {
    await page.getByRole('combobox', { name: '行政区下钻' }).selectOption('110000');
    await expect
      .poll(() => page.evaluate(() => window.__spatialDiagnostics!().adcode))
      .toBe(110000);
    await page.getByRole('button', { name: '全国 / 复位' }).click();
    await expect
      .poll(() => page.evaluate(() => window.__spatialDiagnostics!().adcode))
      .toBe(100000);
    await page.waitForTimeout(70);
    counts.push(
      await page.evaluate(
        () =>
          (window.__spatialDiagnostics!() as unknown as { memory: { geometries: number } }).memory
            .geometries
      )
    );
  }
  expect(Math.max(...counts.slice(-5))).toBeLessThanOrEqual(Math.max(...counts.slice(0, 5)) + 3);
  writeFileSync(test.info().outputPath('gpu-resource-counts.json'), JSON.stringify(counts));
  await test.info().attach('gpu-resource-counts', {
    body: JSON.stringify(counts),
    contentType: 'application/json',
  });
});
