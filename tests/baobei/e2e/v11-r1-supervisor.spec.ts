import { test, expect, action, api, uniq } from './support/isolation';
import type { Page } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';

// V1.1 R1: a supervisor files their own project through the wizard; the
// supervisor cannot review it; a director approves it. Desktop and 390 px.
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

test('V1.1 R1 主管本人报备：向导创建提交、本人不能审核、总监审核通过', async ({ page, cleanup }) => {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await login(page, 'synthetic-manager');
  const name = uniq('V11主管报备');
  await action(page, 'project.create').first().click();
  await expect(page.getByText('主管本人报备，提交后由销售总监审核')).toBeVisible();
  await page.locator('[name=customer]').fill(name + '客户');
  await page.locator('[name=province]').fill('合成省');
  await page.locator('[name=city]').fill('合成市');
  await page.locator('[name=factoryName]').fill('合成一厂');
  await page.locator('[name=projectName]').fill(name);
  await page.locator('[name=informationSource]').selectOption('END_CUSTOMER');
  await action(page, 'project.save-draft').click();
  await expect(page.getByRole('status').filter({ hasText: '草稿已保存' }).first()).toBeVisible();
  await action(page, 'project.step-next').click();
  await page.getByLabel('防摇', { exact: true }).first().check();
  await page.locator('[name=requirementNotes]').fill('主管本人设备需求');
  await action(page, 'project.step-next').click();
  await page.locator('[name=projectType]').selectOption('RETROFIT');
  await page.locator('[name=confidenceLevel]').selectOption('CUSTOMER_CONFIRMED');
  await action(page, 'project.step-next').click();
  await expect(page.getByText('必填信息已齐全。')).toBeVisible();
  const draft = await page.evaluate(() => location.pathname);
  expect(draft).toBeTruthy();
  const listed = await api(page, '/api/v1/projects?q=' + encodeURIComponent(name));
  cleanup.track(listed.body.items[0].id);
  await action(page, 'project.submit').click();
  await expect(page).toHaveURL(/\/projects\/[a-f0-9-]+$/);
  const id = new URL(page.url()).pathname.split('/').pop()!;
  await expect(page.locator('.detail-heading').getByText('已提交', { exact: true })).toBeVisible();
  const mine = (await api(page, '/api/v1/projects/' + id)).body;
  expect(mine).toMatchObject({ ownerSubject: 'synthetic-manager', createdBySubject: 'synthetic-manager', ownerRole: 'baobei_manager', reviewRoute: 'DIRECTOR' });
  await action(page, 'project.tab').filter({ hasText: '审批和重复风险' }).click();
  await expect(page.locator('.review-eligibility')).toContainText('你参与过此项目');
  const denied = await api(page, `/api/v1/projects/${id}/approve`, 'POST', { version: mine.version, reason: '自审尝试' });
  expect(denied.status).toBe(403);
  expect(denied.body.code).toBe('REVIEW_CONFLICT');
  await action(page, 'project.tab').filter({ hasText: '归属历史' }).click();
  await expect(page.getByText('销售总监审核（主管本人报备）')).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);
  await logout(page);

  await login(page, 'synthetic-director');
  await page.goto('/projects/' + id);
  await action(page, 'project.tab').filter({ hasText: '审批和重复风险' }).click();
  await action(page, 'review.approve').click();
  await page.locator('[name=governanceReason]').fill('总监独立审核主管本人报备');
  await action(page, 'governance.confirm').click();
  await expect(page.locator('.detail-heading')).toContainText('已批准');
  await logout(page);
  expect(errors).toEqual([]);
});
