import { test, expect, action } from './support/isolation';
import type { Page } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';

// V1.1 final light UX: 404 for unknown routes behind the same login/permission
// guards; visible keyboard focus on the current navigation entry; phone touch
// targets in the member statistics table.
async function login(p: Page, subject: string) {
  await p.goto('/login');
  await p.locator('select').selectOption(subject);
  await action(p, 'auth.dev-login').click();
  await expect(p.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
}

test('V1.1 未知地址：未登录先去登录页，登录后显示 404 并可返回', async ({ page }) => {
  await page.goto('/no-such-page/123');
  await expect(page).toHaveURL(/\/login$/);
  await login(page, 'synthetic-b');
  await page.goto('/no-such-page/123');
  await expect(page.getByRole('heading', { name: '页面不存在', exact: true })).toBeVisible();
  await expect(page.getByText('/no-such-page/123')).toBeVisible();
  // Still inside the shell (navigation available), and a known route is not a 404.
  await expect(page.getByRole('button', { name: '项目中心', exact: true })).toBeVisible();
  await action(page, 'nav.open').filter({ hasText: '打开项目中心' }).click();
  await expect(page).toHaveURL(/\/projects/);
  await expect(page.getByRole('heading', { name: '页面不存在' })).toHaveCount(0);
  await page.goto('/projects/not-a-project/extra/segments');
  await expect(page.getByRole('heading', { name: '页面不存在', exact: true })).toBeVisible();
  await action(page, 'nav.open').filter({ hasText: '返回工作台' }).click();
  await expect(page.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
});

test('V1.1 权限门禁仍先于 404：销售访问管理地址回到工作台', async ({ page }) => {
  await login(page, 'synthetic-b');
  await page.goto('/settings/teams');
  await expect(page.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: '页面不存在' })).toHaveCount(0);
});

test('V1.1 当前导航项的键盘焦点可见', async ({ page }) => {
  await login(page, 'synthetic-b');
  await page.goto('/projects');
  const current = page.getByRole('button', { name: '项目中心', exact: true });
  await current.focus();
  await page.keyboard.press('Shift+Tab');
  await page.keyboard.press('Tab');
  await expect(current).toBeFocused();
  const shadow = await current.evaluate((e) => getComputedStyle(e).boxShadow);
  expect(shadow).not.toBe('none');
});

test('V1.1 手机端成员统计“查看”按钮触控区域不小于 44px', async ({ page }, info) => {
  test.skip(info.project.name !== 'mobile-390', 'phone layout only');
  await login(page, 'synthetic-manager');
  await page.goto('/statistics');
  const open = page.locator('[data-action-id="statistics.open-member"]').first();
  await expect(open).toBeVisible();
  const box = (await open.boundingBox())!;
  expect(box.height).toBeGreaterThanOrEqual(44);
  expect(box.width).toBeGreaterThanOrEqual(44);
});
