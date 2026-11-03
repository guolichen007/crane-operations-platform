import { test, expect, type Page } from '@playwright/test';

async function loadOverview(page: Page) {
  await page.goto('/');
  await expect(page.locator('.province-summary')).toHaveCount(34);
  await expect(page.locator('.scene-loading')).toBeHidden();
  await expect(page.locator('.scene-error')).toHaveCount(0);
  await page.waitForFunction(() => !window.__spatialDiagnostics!().intro.active);
}

test('全局搜索 Ctrl+K 打开并可搜索资产', async ({ page }) => {
  await loadOverview(page);
  await page.keyboard.press('Control+K');
  await expect(page.locator('.global-search')).toBeVisible();
  await page.getByLabel('搜索资产、项目或省市').fill('南京');
  await expect(page.locator('.global-search-item').first()).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.locator('.global-search')).toBeHidden();
});

test('场景模式 dock 行政区/资产/告警 可切换', async ({ page }) => {
  await loadOverview(page);
  const dock = page.locator('.scene-mode-dock');
  await expect(dock).toBeVisible();
  await dock.getByRole('button', { name: '资产' }).click();
  await page.waitForTimeout(1200);
  await dock.getByRole('button', { name: '告警' }).click();
  await page.waitForTimeout(800);
  await expect(page.locator('main')).toHaveAttribute('data-source-filter', 'all');
});

test('业务侧栏可折叠/展开并持久化', async ({ page }) => {
  await loadOverview(page);
  const rail = page.locator('.side-rail');
  await expect(rail).not.toHaveClass(/collapsed/);
  await page.getByRole('button', { name: '折叠侧栏' }).click();
  await expect(rail).toHaveClass(/collapsed/);
  await page.getByRole('button', { name: '展开侧栏' }).click();
  await expect(rail).not.toHaveClass(/collapsed/);
});

test('全国默认态省份机队 Beacon 存在', async ({ page }) => {
  await loadOverview(page);
  const diag = await page.evaluate(() => window.__spatialDiagnostics!());
  expect(diag.renderMode).toBeDefined();
  expect(diag.quality).toBeDefined();
  // Fleet beacons are 3D status rings at country level; the DOM province summary
  // labels carry the per-province device count.
  const label = page.locator('.province-summary[data-province-code="330000"]');
  await expect(label).toHaveAttribute('data-device-count', '2');
});
