import { test, expect } from '@playwright/test';
import { login } from './login';

// Gate 8: 1280×720 layout regression for the business pages. The existing
// 1366/1920 checks live in the premium specs and are unchanged. Screenshots are
// written as success evidence; failures additionally capture trace/screenshot
// via the B4 config (retain-on-failure).
test('1280×720 业务页面布局回归（资产中心/创建弹窗/详情/位置选择/Equipment 弹窗）', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await login(page, 'b4-admin');

  await page.goto('/#/assets');
  await expect(page.getByTestId('asset-center')).toBeVisible();
  await page.screenshot({ path: 'test-results/b4-visual/1280-asset-center.png', fullPage: true });

  await page.getByTestId('asset-create').click();
  await expect(page.getByTestId('asset-create-form')).toBeVisible();
  await page.screenshot({ path: 'test-results/b4-visual/1280-asset-create.png' });
  const createForm = page.getByTestId('asset-create-form');
  await createForm.getByLabel('授权项目选择').selectOption('P001');
  await createForm.getByPlaceholder('CRN-001').fill('CRN-B4-VISUAL');
  await createForm.getByRole('button', { name: '创建' }).click();

  await expect(page.getByTestId('asset-detail')).toBeVisible();
  await page.screenshot({ path: 'test-results/b4-visual/1280-asset-detail.png', fullPage: true });

  await page.getByRole('button', { name: '移动', exact: true }).click();
  await expect(page.locator('.workspace-modal')).toBeVisible();
  await page.screenshot({ path: 'test-results/b4-visual/1280-move-picker.png' });
  await page.locator('.workspace-modal').getByRole('button', { name: '取消' }).click();

  await page.locator('.tabs').getByRole('button', { name: '设备', exact: true }).click();
  await page.getByRole('button', { name: '安装 Equipment' }).click();
  await expect(page.getByTestId('equipment-create-form')).toBeVisible();
  await page.screenshot({ path: 'test-results/b4-visual/1280-equipment-create.png' });
});
