import { test, expect } from '@playwright/test';
import { login, sessionCsrf } from './login';
import { assertWriteTarget } from './run';

// G3-C: asset management is a small group, and its pages are reachable without
// adding top-level clutter. Runs on the isolated fixture (no writes).
test('侧栏「资产与设备」分组可导航到台账/位置/目录', async ({ page }) => {
  await login(page, 'b4-admin');
  const group = page.getByTestId('rail-assets');
  await expect(group).toBeVisible();
  // The top-level nav still holds the primary business links.
  await expect(page.locator('.side-rail nav > a')).toHaveCount(6);
  // Expand if collapsed, then reach each child route.
  const toggle = group.getByRole('button', { name: '资产与设备' });
  if ((await toggle.getAttribute('aria-expanded')) === 'false') await toggle.click();
  for (const [name, path] of [['资产台账', '/assets'], ['位置中心', '/locations'], ['设备目录', '/catalog']]) {
    await group.getByRole('link', { name }).click();
    await expect(page).toHaveURL(new RegExp(`#${path}$`));
  }
});

// The command-bar scope: a project-filtering page shows the selector; a page that
// does not filter by project shows a fixed "全部已授权项目" label instead.
test('顶栏项目范围：过滤页显示选择器，非过滤页显示「全部已授权项目」固定标签', async ({ page }) => {
  await login(page, 'b4-admin');
  await page.goto('/#/assets');
  await expect(page.getByLabel('当前项目范围')).toBeVisible();
  await expect(page.getByTestId('scope-fixed')).toHaveCount(0);
  await page.goto('/#/catalog');
  await expect(page.getByTestId('scope-fixed')).toContainText('全部已授权项目');
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

// 1280 layout: the asset detail dialogs fit without horizontal overflow and keep
// their primary action visible.
test('1280：资产编辑弹窗不横向溢出且保存可见', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await login(page, 'b4-admin');
  await assertWriteTarget(page);
  const csrf = await sessionCsrf(page);
  const created = await page.evaluate(async ({ csrf }) => {
    const res = await fetch('/api/v1/assets', {
      method: 'POST',
      headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' },
      body: JSON.stringify({ projectId: 'P001', assetType: 'CRANE', assetCode: 'CRN-VIS-' + Math.random().toString(16).slice(2, 8) }),
    });
    return (await res.json()) as { id: string };
  }, { csrf });
  await page.goto(`/#/assets/${created.id}`);
  await page.getByRole('button', { name: '编辑', exact: true }).click();
  const form = page.getByTestId('asset-edit-form');
  await expect(form).toBeVisible();
  await expect(form.getByRole('button', { name: '保存' })).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
});
