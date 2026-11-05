import { test, expect } from '@playwright/test';
import { login, sessionCsrf } from './login';
import { assertWriteTarget, code, createAsset, expectUnobstructed } from './run';

// G3-D3: one navigation hierarchy. Every business page appears exactly once in
// the 业务导航 landmark; asset management is a disclosure group inside it with a
// correct aria-expanded, keyboard operable, and still reachable when collapsed.
// Read-only (no writes).
test('侧栏：单一导航层级、资产分组可键盘展开收起、折叠后仍可达', async ({ page }) => {
  await login(page, 'b4-admin');
  const nav = page.getByRole('navigation', { name: '业务导航' });
  const hrefs = await nav.locator('a').evaluateAll((as) => as.map((a) => a.getAttribute('href')));
  expect(new Set(hrefs).size, '每个业务页面只出现一次').toBe(hrefs.length);
  expect(hrefs.filter((h) => h === '#/assets')).toHaveLength(1);
  for (const path of ['#/assets', '#/locations', '#/catalog']) expect(hrefs).toContain(path);

  const toggle = nav.getByRole('button', { name: '资产与设备' });
  if ((await toggle.getAttribute('aria-expanded')) === 'false') await toggle.click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  const group = nav.getByRole('group', { name: '资产与设备' });
  for (const [name, path] of [['资产台账', '/assets'], ['位置中心', '/locations'], ['设备目录', '/catalog']]) {
    await group.getByRole('link', { name }).click();
    await expect(page).toHaveURL(new RegExp(`#${path}$`));
    await expect(group.getByRole('link', { name })).toHaveClass(/active/);
  }

  // Keyboard: focus the disclosure and toggle it with Enter / Space.
  await page.goto('/#/overview');
  await toggle.focus();
  await page.keyboard.press('Enter');
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await expect(group).toHaveCount(0);
  await page.keyboard.press('Space');
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  await expect(group).toBeVisible();

  // Collapsed rail: the group pages remain reachable as labelled icon links.
  await page.getByRole('button', { name: '折叠侧栏' }).click();
  await expect(nav.getByRole('link', { name: '位置中心' })).toBeVisible();
  await nav.getByRole('link', { name: '位置中心' }).click();
  await expect(page).toHaveURL(/#\/locations$/);
  await page.getByRole('button', { name: '展开侧栏' }).click();
});

// The command-bar scope at every common desktop width (1280/1366/1440/1920):
// on a filtering page the selector is visible, unobstructed and operable —
// choosing a project narrows the current page (the route is kept), clearing it
// returns to all authorized projects. The overview's selector only focuses the
// map and is labelled 地图聚焦项目; pages without project filtering show a fixed
// "全部已授权项目" label and no selector. Read-only.
for (const [w, h] of [[1280, 720], [1366, 768], [1440, 900], [1920, 1080]] as const) {
  test(`顶栏项目范围 ${w}×${h}：过滤页可选择/清除且保持当前页，总览为聚焦，非过滤页为固定标签`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: h });
    await login(page, 'b4-admin');
    await page.goto('/#/assets');
    const scope = page.getByLabel('当前项目范围');
    await expectUnobstructed(page, scope, '当前项目范围');
    await expect(page.getByTestId('scope-fixed')).toHaveCount(0);
    await scope.selectOption('P001');
    await expect(page).toHaveURL(/#\/assets\?project=P001$/);
    await expect(scope).toHaveValue('P001');
    await expect(page.getByTestId('asset-center')).toBeVisible();
    await scope.selectOption('');
    await expect(page).toHaveURL(/#\/assets$/);
    await expect(scope).toHaveValue('');

    await page.goto('/#/catalog');
    await expect(page.getByTestId('scope-fixed')).toContainText('全部已授权项目');
    await expect(page.getByLabel('当前项目范围')).toHaveCount(0);
    await expect(page.getByLabel('地图聚焦项目')).toHaveCount(0);

    await page.goto('/#/overview');
    const focus = page.getByLabel('地图聚焦项目');
    await expectUnobstructed(page, focus, '地图聚焦项目');
    await expect(page.getByLabel('当前项目范围')).toHaveCount(0);
    await focus.selectOption('P001');
    await expect(page).toHaveURL(/#\/overview\?project=P001$/);
    await focus.selectOption('');
    await expect(page).toHaveURL(/#\/overview$/);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
  });
}

// Asset ledger: an out-of-scope ?project= shows an explicit message and never a
// false "暂无资产" empty table.
test('资产台账：未授权项目范围显示明确提示而非空表', async ({ page }) => {
  await login(page, 'b4-admin');
  await page.goto('/#/assets?project=P999-unauthorized');
  await expect(page.getByTestId('asset-center')).toBeVisible();
  await expect(page.getByTestId('asset-scope-unauthorized')).toBeVisible();
  await expect(page.getByTestId('asset-table')).toHaveCount(0);
});

// Detail → back keeps the ledger context (authorized project scope, search,
// page). Writes one asset with the run prefix.
test('资产详情返回台账：保留合法项目范围与搜索条件', async ({ page }) => {
  await login(page, 'b4-admin');
  await assertWriteTarget(page);
  const csrf = await sessionCsrf(page);
  const assetCode = code('CRN', 'BACK');
  // No displayName: the server falls back to the code, so the code appears in two
  // cells of the row — rows are located by their stable data-asset-id instead.
  const created = await createAsset(page, csrf, { projectId: 'P001', assetType: 'CRANE', assetCode });
  await page.goto(`/#/assets?project=P001&q=${encodeURIComponent(assetCode)}`);
  await expect(page.locator('.asset-filters input')).toHaveValue(assetCode);
  const rows = page.getByTestId('asset-table').locator('tbody tr');
  await expect(rows.filter({ has: page.locator('td[data-col="assetCode"]', { hasText: assetCode }) })).toHaveCount(1);
  const row = page.locator(`tr[data-asset-id="${created.id}"]`);
  await expect(row).toHaveCount(1);
  await expect(row.locator('td[data-col="assetCode"]')).toHaveText(assetCode);
  await row.click();
  await expect(page).toHaveURL(new RegExp(`#/assets/${created.id}$`));
  await expect(page.getByTestId('asset-detail')).toHaveAttribute('data-asset-id', created.id);
  await expect(page.getByTestId('asset-detail')).toHaveAttribute('data-project-id', 'P001');
  await page.getByRole('button', { name: '返回' }).click();
  await expect(page).toHaveURL(/#\/assets\?/);
  const url = new URL(page.url().replace('#/', ''));
  expect(url.searchParams.get('project')).toBe('P001');
  expect(url.searchParams.get('q')).toBe(assetCode);
  await expect(page.getByTestId('asset-table')).toContainText(assetCode);
});

// 1280 layout: the asset detail dialogs fit without horizontal overflow and keep
// their primary action visible. Writes one asset with the run prefix.
test('1280：资产编辑弹窗不横向溢出且保存可见', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await login(page, 'b4-admin');
  await assertWriteTarget(page);
  const csrf = await sessionCsrf(page);
  const created = await createAsset(page, csrf, { projectId: 'P001', assetType: 'CRANE', assetCode: code('CRN', 'NAV1280') });
  await page.goto(`/#/assets/${created.id}`);
  await page.getByRole('button', { name: '编辑', exact: true }).click();
  const form = page.getByTestId('asset-edit-form');
  await expect(form).toBeVisible();
  await expect(form.getByRole('button', { name: '保存' })).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
});
