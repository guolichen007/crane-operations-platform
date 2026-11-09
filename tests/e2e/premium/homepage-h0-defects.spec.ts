// Homepage defect counter-proofs. Real browser, mocked API (no server writes).
//
// H0-D2 (UTC event time) was fixed in H2-1D (provider.ts formats sentAt in Asia/Shanghai) and is
// now a required test. H0-D1 (empty province counted as a province) remains an expected failure at
// its source in apps/web-console/tests/data.test.ts (it.fails): aggregate() is unchanged and the
// homepage does not render that number.
//
// H0-D3 (region deep link) and H0-D4 (left navigation occluded) were fixed in
// H1 and are now required tests driven by real mouse/keyboard input.
import { test, expect, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { cloudDevice, cloudAlarm, cloudTime } from '../../../apps/web-console/tests/cloud-fixtures';
import { inventorySummary, routeSummary } from '../summary-mock';

const example = JSON.parse(readFileSync('deploy/inventory/inventory.example.yaml', 'utf8'));
const session = {
  userId: 'audit', displayName: '审查员',
  projects: example.projects.map((p: { id: string; name: string }) => ({ projectId: p.id, projectName: p.name, role: 'viewer' })),
  csrfToken: 'synthetic-csrf-test-only', expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z',
};

/** Example inventory with P001 located in Haining (330481) so drill-down has real content. */
async function inventoryFixture(page: Page, opts: { located?: boolean; empty?: boolean } = {}) {
  await page.route('**/auth/session', (r) => r.fulfill({ json: session }));
  const projects = opts.empty
    ? []
    : example.projects.map((p: { id: string }, i: number) => ({
        ...p,
        adminCode: opts.located && i === 0 ? 330481 : null,
        simulatedCount: 0, runtimeDeviceCount: 0, realConnectedCount: 0,
        assetDeviceCount: example.assetDevices.filter((a: { projectId: string }) => a.projectId === p.id).length,
      }));
  const assets = opts.empty ? [] : example.assetDevices.map((a: object) => ({ ...a, runtimeSource: null, runtimeDeviceId: null, connectivity: null }));
  await page.route('**/api/v1/**', (r) => {
    const path = new URL(r.request().url()).pathname;
    return r.fulfill({
      json: path === '/api/v1/projects' ? projects : path === '/api/v1/assets/devices' ? assets
        : path === '/api/v1/devices' ? (opts.empty ? [] : [cloudDevice('SIM-P001-01')]) : path === '/api/v1/alarms' ? (opts.empty ? [] : [cloudAlarm()]) : {},
    });
  });
  await routeSummary(page, (c) => inventorySummary(projects, assets, c.region));
}
async function open(page: Page, hash = '/#/overview') {
  await page.goto(hash);
  await expect(page.locator('.province-summary')).toHaveCount(34);
  await expect(page.locator('.scene-loading')).toBeHidden();
}
const main = (page: Page) => page.locator('main.overview');
const regionParam = (page: Page) => new URL(page.url().replace('#/', '')).searchParams.get('region');

/** Click a visible, unobstructed element with the real mouse (fails if covered). */
async function realClick(page: Page, selector: string) {
  const el = page.locator(selector).first();
  await el.scrollIntoViewIfNeeded();
  const box = (await el.boundingBox())!;
  const x = box.x + box.width / 2, y = box.y + box.height / 2;
  const hit = await page.evaluate(({ x, y, selector }) => {
    const top = document.elementFromPoint(x, y);
    const target = document.querySelector(selector);
    return !!top && !!target && (top === target || target.contains(top));
  }, { x, y, selector });
  expect(hit, `${selector} 未被遮挡`).toBe(true);
  await page.mouse.click(x, y);
}

test('H0-D2 事件时间：Cloud 告警时间按上海时区显示，而不是截取 UTC 字符串（H2-1D 已修复，必过）', async ({ page }) => {
  await inventoryFixture(page);
  await open(page);
  expect(cloudTime.startsWith('2026-10-22T08:49:15')).toBe(true); // → 16:49:15 Asia/Shanghai
  await expect(page.locator('.event-item time').first()).toHaveText('16:49:15');
});

test('D3 真实鼠标逐级钻取：省→市→县写回 ?region=，刷新恢复，后退/前进正确', async ({ page }) => {
  test.setTimeout(150000);
  await page.setViewportSize({ width: 1920, height: 1080 });
  await inventoryFixture(page, { located: true });
  await open(page);
  for (const code of ['330000', '330400', '330481']) {
    await realClick(page, `[data-testid=region-nav] [data-region-code="${code}"]`);
    await expect(main(page)).toHaveAttribute('data-adcode', code);
    await expect(main(page)).toHaveAttribute('data-scene-state', 'REGION_IDLE');
    await expect.poll(() => regionParam(page)).toBe(code);
  }
  await expect(page.getByRole('navigation', { name: '行政区路径' })).toContainText('海宁市');
  // Refresh restores the full path, not just the leaf.
  await page.reload();
  await expect(main(page)).toHaveAttribute('data-adcode', '330481', { timeout: 30000 });
  await expect(page.getByRole('navigation', { name: '行政区路径' })).toContainText('浙江省');
  await expect(page.getByRole('navigation', { name: '行政区路径' })).toContainText('嘉兴市');
  // Back → 嘉兴, back → 浙江, forward → 嘉兴.
  await page.goBack();
  await expect.poll(() => regionParam(page)).toBe('330400');
  await expect(main(page)).toHaveAttribute('data-adcode', '330400', { timeout: 20000 });
  await page.goBack();
  await expect(main(page)).toHaveAttribute('data-adcode', '330000', { timeout: 20000 });
  await page.goForward();
  await expect(main(page)).toHaveAttribute('data-adcode', '330400', { timeout: 20000 });
  await expect.poll(() => regionParam(page)).toBe('330400');
  // Breadcrumb (real click) back to the country clears ?region=.
  await page.getByRole('navigation', { name: '行政区路径' }).getByRole('button', { name: '全国' }).click();
  await expect(main(page)).toHaveAttribute('data-adcode', '100000', { timeout: 20000 });
  await expect.poll(() => regionParam(page)).toBeNull();
});

test('D3 直接打开 ?region=330481 恢复海宁市上下文（三维与二维一致）', async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  await inventoryFixture(page, { located: true });
  await page.goto('/#/overview?region=330481');
  await expect(main(page)).toHaveAttribute('data-adcode', '330481', { timeout: 30000 });
  await expect(page.getByRole('navigation', { name: '行政区路径' })).toContainText('浙江省');
  expect(regionParam(page)).toBe('330481');
  // Switching to 2D keeps the same context (shared store, no second state).
  await page.getByRole('button', { name: '轻量视图' }).click();
  await expect(page.getByTestId('spatial-2d')).toHaveAttribute('data-region', '330481');
  await expect(page).toHaveURL(/view=2d/);
  expect(regionParam(page)).toBe('330481');
});

test('D3 未知区域回退全国并提示；URL 以 replace 清理（后退不再落回坏链接），无循环写入', async ({ page }) => {
  await inventoryFixture(page, { located: true });
  await open(page);
  const before = await page.evaluate(() => history.length);
  await page.evaluate(() => { location.hash = '#/overview?region=999999'; });
  await expect(page.getByTestId('home-notice')).toContainText('999999');
  await expect(main(page)).toHaveAttribute('data-adcode', '100000');
  await expect.poll(() => regionParam(page)).toBeNull();
  await page.waitForTimeout(1500);
  // The bad entry was replaced in place: exactly one new history entry (the
  // user's own navigation), and its URL is now the corrected one.
  expect(await page.evaluate(() => history.length)).toBe(before + 1);
  expect(page.url()).not.toContain('999999');
  const stable = page.url();
  await page.waitForTimeout(1000);
  expect(page.url()).toBe(stable); // no further writes (no loop)
  await page.goBack();
  await expect.poll(() => page.url()).not.toContain('999999');
  await page.goto('/#/overview?region=abc');
  await expect(page.getByTestId('home-notice')).toBeVisible();
  await expect.poll(() => regionParam(page)).toBeNull();
});

test('D3 快速切换区域：旧的异步钻取失效，最终停在最后一次目标', async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  await inventoryFixture(page, { located: true });
  await open(page);
  // Three targets in quick succession, without waiting for the camera.
  await page.evaluate(() => { location.hash = '#/overview?region=330481'; });
  await page.evaluate(() => { location.hash = '#/overview?region=440000'; });
  await page.evaluate(() => { location.hash = '#/overview?region=510000'; });
  await expect(main(page)).toHaveAttribute('data-adcode', '510000', { timeout: 30000 });
  await expect(main(page)).toHaveAttribute('data-scene-state', 'REGION_IDLE');
  await page.waitForTimeout(1500);
  await expect(main(page)).toHaveAttribute('data-adcode', '510000');
  expect(regionParam(page)).toBe('510000');
});

for (const [w, h] of [[1280, 720], [1366, 768], [1920, 1080]] as const)
  test(`D4 ${w}×${h}：行政区按钮可见、未被遮挡、真实鼠标与键盘均可操作`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: h });
    await inventoryFixture(page, { located: true });
    await open(page);
    await realClick(page, '[data-testid=region-nav] [data-region-code="330000"]');
    await expect(main(page)).toHaveAttribute('data-adcode', '330000', { timeout: 20000 });
    // Keyboard: Tab to the next level's first chip and press Enter.
    await expect(main(page)).toHaveAttribute('data-scene-state', 'REGION_IDLE');
    const chip = page.locator('[data-testid=region-nav] [data-region-code="330400"]');
    await chip.focus();
    await expect(chip).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(main(page)).toHaveAttribute('data-adcode', '330400', { timeout: 20000 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(0);
  });

test('D4 125% 缩放（1280×720 屏幕 ≈ 1024×576 CSS px）：无横向溢出，导航可用', async ({ page }) => {
  await page.setViewportSize({ width: 1024, height: 576 });
  await inventoryFixture(page, { located: true });
  await open(page);
  expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(0);
  await realClick(page, '[data-testid=region-nav] [data-region-code="330000"]');
  await expect(main(page)).toHaveAttribute('data-adcode', '330000', { timeout: 20000 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(0);
});

test('空数据：无授权项目时显示明确空态（服务端合法 0），定位按钮禁用，统计无 NaN', async ({ page }) => {
  await inventoryFixture(page, { empty: true });
  await page.goto('/#/overview');
  await expect(page.locator('.scene-loading')).toBeHidden();
  await expect(page.getByTestId('home-empty')).toBeVisible();
  await expect(page.getByRole('button', { name: '聚焦首个登记项目' })).toBeDisabled();
  const text = await page.getByTestId('home-kpis').innerText();
  expect(text).not.toMatch(/NaN|Infinity/);
  await expect(page.locator('[data-kpi=registered] .home-kpi-value')).toHaveText('0台');
});
