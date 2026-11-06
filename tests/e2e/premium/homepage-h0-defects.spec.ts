// H0 homepage audit (baseline d97c0a7): reproductions of confirmed defects on the
// current Overview. Each is marked test.fail(): it PASSES in CI while the defect
// exists, and turns red as soon as H1/H2 fixes it — the marker must then be
// removed so the test becomes a regular guard. Real browser, mocked API (no
// server writes). See docs/04-前端与可视化/Homepage-H0-首页审查与布局设计.md.
import { test, expect, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { cloudDevice, cloudAlarm, cloudTime } from '../../../apps/web-console/tests/cloud-fixtures';

const example = JSON.parse(readFileSync('deploy/inventory/inventory.example.yaml', 'utf8'));
const session = {
  userId: 'audit', displayName: '审查员',
  projects: example.projects.map((p: { id: string; name: string }) => ({ projectId: p.id, projectName: p.name, role: 'viewer' })),
  csrfToken: 'synthetic-csrf-test-only', expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z',
};

/** The anonymous example inventory as served: no project has a province configured. */
async function inventoryFixture(page: Page) {
  await page.route('**/auth/session', (r) => r.fulfill({ json: session }));
  const projects = example.projects.map((p: { id: string }) => ({
    ...p, adminCode: null, simulatedCount: 0, runtimeDeviceCount: 0, realConnectedCount: 0,
    assetDeviceCount: example.assetDevices.filter((a: { projectId: string }) => a.projectId === p.id).length,
  }));
  const assets = example.assetDevices.map((a: object) => ({ ...a, runtimeSource: null, runtimeDeviceId: null, connectivity: null }));
  await page.route('**/api/v1/**', (r) => {
    const path = new URL(r.request().url()).pathname;
    return r.fulfill({
      json: path === '/api/v1/projects' ? projects : path === '/api/v1/assets/devices' ? assets
        : path === '/api/v1/devices' ? [cloudDevice('SIM-P001-01')] : path === '/api/v1/alarms' ? [cloudAlarm()] : {},
    });
  });
}
async function open(page: Page) {
  await page.goto('/#/overview');
  await expect(page.locator('.province-summary')).toHaveCount(34);
  await expect(page.locator('.scene-loading')).toBeHidden();
}

test('H0-D1 省级覆盖：所有项目均未配置省份时，不得显示「覆盖 1 个省级区域」', async ({ page }) => {
  test.fail(true, 'H0-D1: aggregate() counts the empty province "" as one province');
  await inventoryFixture(page);
  await open(page);
  // Every example project has province "" — the true count of covered provinces is 0.
  expect(example.projects.every((p: { province: string }) => p.province === '')).toBe(true);
  await expect(page.locator('.kpi-strip')).not.toContainText('覆盖 1 个省级区域');
});

test('H0-D2 事件时间：Cloud 告警时间按上海时区显示，而不是截取 UTC 字符串', async ({ page }) => {
  test.fail(true, 'H0-D2: provider.ts renders sentAt.slice(11, 19), i.e. UTC, next to a Shanghai clock');
  await inventoryFixture(page);
  await open(page);
  // cloudTime = 2026-10-22T08:49:15Z → 16:49:15 in Asia/Shanghai.
  expect(cloudTime.startsWith('2026-10-22T08:49:15')).toBe(true);
  await expect(page.locator('.event-item time').first()).toHaveText('16:49:15');
});

test('H0-D3 地图钻取写回 URL：进入省级后地址带 ?region=，刷新可恢复', async ({ page }) => {
  test.fail(true, 'H0-D3: ?region= is read on load but never written back on drill-down (one-way link)');
  await inventoryFixture(page);
  await open(page);
  // The chip is occluded by the event strip at this size (H0-D4), so the drill is
  // triggered through the DOM to isolate this defect from that one.
  await page.locator('.region-chips button').first().dispatchEvent('click');
  await expect(page.locator('main.overview')).not.toHaveAttribute('data-adcode', '100000');
  await expect(page).toHaveURL(/[?&]region=\d{6}/, { timeout: 5000 });
});

test('H0-D4 1366×768：行政区导航入口未被底部事件条遮挡', async ({ page }) => {
  test.fail(true, 'H0-D4: left-context sections overlap (chips under .source-filter after scroll; under .event-strip before) at 1280–1920');
  await page.setViewportSize({ width: 1366, height: 768 });
  await inventoryFixture(page);
  await open(page);
  const chip = page.locator('.region-chips button').first();
  await chip.scrollIntoViewIfNeeded();
  const box = (await chip.boundingBox())!;
  const topmost = await page.evaluate(({ x, y }) => {
    const el = document.elementFromPoint(x, y) as HTMLElement | null;
    return el?.closest('.region-chips') ? 'chip' : el?.className ?? 'none';
  }, { x: box.x + box.width / 2, y: box.y + box.height / 2 });
  expect(topmost).toBe('chip');
});
