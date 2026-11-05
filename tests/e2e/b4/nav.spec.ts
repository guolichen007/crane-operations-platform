import { test, expect } from '@playwright/test';
import { login, sessionCsrf } from './login';
import { assertWriteTarget, code } from './run';

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

// The command-bar scope: a project-filtering page shows the selector; a page that
// does not filter by project shows a fixed "全部已授权项目" label instead.
test('顶栏项目范围：过滤页显示选择器，非过滤页显示固定标签，总览为「地图聚焦项目」', async ({ page }) => {
  await login(page, 'b4-admin');
  await page.goto('/#/assets');
  await expect(page.getByLabel('当前项目范围')).toBeVisible();
  await expect(page.getByTestId('scope-fixed')).toHaveCount(0);
  await page.goto('/#/catalog');
  await expect(page.getByTestId('scope-fixed')).toContainText('全部已授权项目');
  await expect(page.getByLabel('当前项目范围')).toHaveCount(0);
  // The overview only focuses the map on a project; it is labelled as such.
  await page.goto('/#/overview');
  await expect(page.getByLabel('地图聚焦项目')).toBeVisible();
  await expect(page.getByLabel('当前项目范围')).toHaveCount(0);
});

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
  await page.evaluate(async ({ csrf, assetCode }) => {
    await fetch('/api/v1/assets', {
      method: 'POST',
      headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' },
      body: JSON.stringify({ projectId: 'P001', assetType: 'CRANE', assetCode }),
    });
  }, { csrf, assetCode });
  await page.goto(`/#/assets?project=P001&q=${encodeURIComponent(assetCode)}`);
  await expect(page.locator('.asset-filters input')).toHaveValue(assetCode);
  await page.getByTestId('asset-table').getByText(assetCode).click();
  await expect(page.getByTestId('asset-detail')).toBeVisible();
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
  const created = await page.evaluate(async ({ csrf, assetCode }) => {
    const res = await fetch('/api/v1/assets', {
      method: 'POST',
      headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' },
      body: JSON.stringify({ projectId: 'P001', assetType: 'CRANE', assetCode }),
    });
    return (await res.json()) as { id: string };
  }, { csrf, assetCode: code('CRN', 'VIS1280') });
  await page.goto(`/#/assets/${created.id}`);
  await page.getByRole('button', { name: '编辑', exact: true }).click();
  const form = page.getByTestId('asset-edit-form');
  await expect(form).toBeVisible();
  await expect(form.getByRole('button', { name: '保存' })).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
});
