import { test, expect, action, api, uniq } from './support/isolation';
import type { Page } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';

// V1.1 R5 (AC-19, AC-20): customer center, contacts, project link, and the
// other team sees nothing but a fixed notice.
async function login(p: Page, subject: string) {
  await p.goto('/login');
  await p.locator('select').selectOption(subject);
  await action(p, 'auth.dev-login').click();
  await expect(p.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
}
async function logout(p: Page) {
  await action(p, 'auth.account').click();
  await action(p, 'auth.logout').click();
  await expect(p).toHaveURL('/login');
}

test('V1.1 R5 客户中心：同客户多项目、联系人、项目关联、跨团队不泄漏', async ({ page }) => {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await login(page, 'synthetic-a');
  const tag = uniq('V11客户');
  const customer = tag + '北岭重工有限公司';
  for (const n of ['一期', '二期']) {
    const r = await api(page, '/api/v1/projects', 'POST', { projectName: tag + n, customer, salesStage: 'L1_REQUIREMENT', version: 1 });
    expect(r.status).toBe(200);
  }
  await action(page, 'nav.open').filter({ hasText: '客户中心' }).click();
  await page.getByLabel('搜索客户').fill(tag);
  const card = action(page, 'customer.open').filter({ hasText: customer });
  await expect(card).toContainText('项目 2');
  await card.click();
  await expect(page.getByRole('heading', { name: customer })).toBeVisible();
  await expect(page.locator('[aria-label="相关项目"] li')).toHaveCount(2);
  // Team profile and a contact.
  await action(page, 'customer.edit').click();
  await page.locator('[name=industry]').fill('重型机械');
  await action(page, 'customer.confirm').click();
  await expect(page.locator('.page-heading')).toContainText('重型机械');
  await action(page, 'contact.create').click();
  await page.locator('[name=contactName]').fill('王工');
  await page.locator('[name=contactTitle]').fill('设备科长');
  await page.locator('[name=contactPhone]').fill('13800001111');
  await action(page, 'contact.confirm').click();
  await expect(page.locator('.contact-list')).toContainText('王工');
  await expect(page.locator('.contact-list')).toContainText('13800001111');
  // Link the contact from the project page.
  await page.locator('[aria-label="相关项目"] [data-action-id="project.detail"]').first().click();
  await expect(page.locator('.project-contacts')).toContainText(customer);
  await page.getByLabel('选择要关联的联系人').selectOption({ label: `${customer} · 王工（设备科长）` });
  await action(page, 'project-contact.link').click();
  await expect(page.locator('.project-contacts .contact-list')).toContainText('王工');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  const id = (await api(page, '/api/v1/customers?q=' + encodeURIComponent(tag))).body.items[0].id;
  await logout(page);

  // Other team: nothing in list or detail; creating the same name shows only a
  // fixed notice and an empty team-private view.
  await login(page, 'synthetic-c');
  await page.goto('/customers?q=' + encodeURIComponent(tag));
  await expect(page.getByText('没有符合条件的客户')).toBeVisible();
  await page.goto('/customers/' + id);
  await expect(page.getByRole('alert')).toContainText('客户不存在或不在你的范围内');
  await expect(page.locator('body')).not.toContainText('王工');
  await page.goto('/customers');
  await action(page, 'customer.create').click();
  await page.locator('[name=customerName]').fill(customer);
  await expect(page.getByTestId('customer-restricted')).toContainText('请联系管理员');
  await expect(page.getByRole('dialog')).not.toContainText('重型机械');
  await action(page, 'customer.confirm').click();
  await expect(page.getByRole('heading', { name: customer })).toBeVisible();
  await expect(page.locator('[aria-label="相关项目"]')).toContainText('你的范围内没有涉及该客户的项目');
  await expect(page.locator('body')).not.toContainText('王工');
  await expect(page.locator('body')).not.toContainText('重型机械');
  expect(errors).toEqual([]);
});
