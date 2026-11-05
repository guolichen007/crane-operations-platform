import { fileURLToPath } from 'node:url';
import {
  test,
  expect,
  type Page,
} from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
const action = (p: Page, id: string) => p.locator(`[data-action-id="${id}"]:visible`);
async function login(p: Page, subject = 'synthetic-a') {
  await p.goto('/login');
  await p.locator('select').selectOption(subject);
  await action(p, 'auth.dev-login').click();
  await expect(p.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
}
async function stepOne(p: Page, name: string, customer: string) {
  await p.locator('[name=customer]').fill(customer);
  await p.locator('[name=province]').fill('合成省');
  await p.locator('[name=city]').fill('合成市');
  await p.locator('[name=factoryName]').fill('合成一厂');
  await p.locator('[name=projectName]').fill(name);
}
async function remaining(p: Page) {
  await action(p, 'project.step-next').click();
  await p.locator('[name=projectType]').selectOption('RETROFIT');
  await p.getByLabel('防摇', { exact: true }).check();
  await p.locator('[name=requirementNotes]').fill('Synthetic 设备防摇需求');
  await action(p, 'project.step-back').click();
  await expect(p.locator('[name=customer]')).toBeVisible();
  await action(p, 'project.step-next').click();
  await action(p, 'project.step-next').click();
  await p.locator('[name=informationSource]').selectOption('END_CUSTOMER');
  await p.locator('[name=confidenceLevel]').selectOption('CUSTOMER_CONFIRMED');
}
async function api(p: Page, path: string, method = 'GET', body?: unknown) {
  return p.evaluate(
    async ({ path, method, body }) => {
      const me = await (await fetch('/api/v1/me')).json();
      const r = await fetch(path, {
        method,
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': me.csrfToken, 'Idempotency-Key': crypto.randomUUID() },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      return { status: r.status, body: await r.json() };
    },
    { path, method, body }
  );
}
test('flow: draft, edit, submit, preview, followup, list, actions', async ({ page }, info) => {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await login(page);
  const name = 'Synthetic闭环-' + info.project.name + '-' + Date.now(),
    customer = 'Synthetic客户-' + Date.now();
  await action(page, 'project.create').first().click();
  await stepOne(page, name, customer);
  await action(page, 'project.save-draft').click();
  await expect(page.getByRole('status').filter({ hasText: '草稿已保存' }).first()).toBeVisible();
  await page.getByRole('button', { name: '我的报备', exact: true }).click();
  await page.getByRole('textbox', { name: '搜索报备', exact: true }).fill(name);
  await expect(page.locator('h1 small')).toHaveText('1');
  await action(page, 'project.preview').first().click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.keyboard.press('Tab');
  await expect(page.getByRole('dialog').locator(':focus')).toHaveCount(1);
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(action(page, 'project.preview').first()).toBeFocused();
  await action(page, 'project.preview').first().click();
  await action(page, 'project.preview-close').click();
  await action(page, 'project.preview').first().click();
  await action(page, 'project.detail').click();
  await expect(page.getByRole('heading', { name, exact: true })).toBeVisible();
  await action(page, 'project.edit').click();
  await remaining(page);
  await action(page, 'project.submit').click();
  await expect(page).toHaveURL(/\/projects\/[a-f0-9-]+$/);
  await expect(page.locator('.detail-heading').getByText('已提交', { exact: true })).toBeVisible();
  await action(page, 'activity.open').click();
  await expect(action(page, 'activity.create')).toBeDisabled();
  await action(page, 'dialog.cancel').click();
  await action(page, 'activity.open').click();
  await page.locator('[name=summary]').fill('Synthetic 电话确认');
  await page.locator('[name=nextAction]').fill('发送 synthetic 方案');
  await page.locator('[name=nextFollowupAt]').fill('2027-01-01T10:00');
  await action(page, 'activity.create').click();
  await expect(
    page.getByRole('heading', { name: 'Synthetic 电话确认', exact: true })
  ).toBeVisible();
  await action(page, 'project.tab').filter({ hasText: '审计历史' }).click();
  await expect(page.getByText('记录跟进', { exact: true }).last()).toBeVisible();
  await action(page, 'project.tab').filter({ hasText: '概况' }).click();
  await expect(page.getByText('发送 synthetic 方案', { exact: true })).toBeVisible();
  await action(page, 'nav.open').filter({ hasText: '返回我的报备' }).click();
  await page.getByRole('textbox', { name: '搜索报备', exact: true }).fill(name);
  await expect(page.locator('h1 small')).toHaveText('1');
  await expect(
    page.locator(info.project.name === 'mobile-390' ? '.mobile-projects' : '.table-wrap')
  ).toContainText('发送 synthetic 方案');
  await action(page, 'project.sort').click();
  await action(page, 'project.columns').click();
  await page.getByLabel('负责人', { exact: true }).uncheck();
  await expect(action(page, 'project.page-prev')).toBeDisabled();
  await expect(action(page, 'project.page-next')).toBeDisabled();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth);
  expect(overflow).toBe(false);
  await page.screenshot({
    path: fileURLToPath(
      new URL(`../../../local-private/baobei/${info.project.name}-list.png`, import.meta.url)
    ),
    fullPage: true,
  });
  await action(page, 'auth.logout').click();
  await expect(page).toHaveURL('/login');
  expect(errors).toEqual([]);
});
test('duplicate warning and sales B cannot guess A UUID', async ({ page, browser }, info) => {
  await login(page);
  const name = 'Synthetic重复-' + info.project.name + '-' + Date.now(),
    customer = 'Synthetic重复客户-' + Date.now();
  await action(page, 'project.create').first().click();
  await stepOne(page, name, customer);
  await remaining(page);
  await action(page, 'project.submit').click();
  await expect(page).toHaveURL(/\/projects\/[a-f0-9-]+$/);
  const id = page.url().split('/').pop()!;
  const other = await browser.newContext({
    baseURL: 'http://127.0.0.1:5188',
    viewport: info.project.use.viewport,
  });
  const b = await other.newPage();
  await login(b, 'synthetic-b');
  const res = await api(b, '/api/v1/projects/' + id);
  expect(res.status).toBe(404);
  await action(b, 'project.create').first().click();
  await stepOne(b, name, customer);
  await expect(b.getByText('发现疑似已有报备，提交后由主管确认')).toBeVisible();
  await remaining(b);
  await action(b, 'project.submit').click();
  await expect(b.locator('.detail-heading').getByText('重复待审核', { exact: true })).toBeVisible();
  await other.close();
});
test('guard, readable error, input retained and loading prevents duplicate', async ({ page }) => {
  await login(page);
  await action(page, 'project.create').first().click();
  await page.route('**/api/v1/projects', async (route) => {
    if (route.request().method() === 'POST') {
      await new Promise((r) => setTimeout(r, 300));
      await route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({ message: '合成测试暂时不可用' }),
      });
    } else await route.continue();
  });
  await page.locator('[name=customer]').fill('Synthetic未保存');
  await action(page, 'project.save-draft').click();
  await expect(action(page, 'project.save-draft')).toBeDisabled();
  await expect(page.getByRole('alert')).toContainText('合成测试暂时不可用');
  await expect(page.locator('[name=customer]')).toHaveValue('Synthetic未保存');
  await page.getByRole('button', { name: '我的报备', exact: true }).click({ noWaitAfter: true });
  await expect(page.getByRole('dialog')).toBeVisible();
  await action(page, 'guard.stay').click();
  await expect(page.locator('[name=customer]')).toHaveValue('Synthetic未保存');
  await page.getByRole('button', { name: '我的报备', exact: true }).click({ noWaitAfter: true });
  await action(page, 'guard.leave').click();
  await expect(page.getByRole('heading', { name: /我的报备/ })).toBeVisible();
});

test('pagination and read-only navigation actions', async ({ page }, info) => {
  await login(page);
  const prefix = 'Synthetic分页-' + info.project.name + '-' + Date.now();
  for (let i = 0; i < 21; i++) {
    const r = await api(page, '/api/v1/projects', 'POST', {
      projectName: prefix + '-' + i,
      customer: 'Synthetic分页客户',
    });
    expect(r.status).toBe(200);
  }
  await page.getByRole('button', { name: '我的报备', exact: true }).click();
  await page.getByRole('textbox', { name: '搜索报备', exact: true }).fill(prefix);
  await expect(page.locator('h1 small')).toHaveText('21');
  await action(page, 'project.page-next').click();
  await expect(page.locator('.pagination')).toContainText('第 2 页');
  await expect(action(page, 'project.page-next')).toBeDisabled();
  await action(page, 'project.page-prev').click();
  await expect(page.locator('.pagination')).toContainText('第 1 页');
  await action(page, 'project.preview').first().click();
  await action(page, 'project.detail').click();
  for (const tab of ['关系方', '设备与需求', '跟进记录', '重复与审批', '审计历史']) {
    await action(page, 'project.tab').filter({ hasText: tab }).click();
    await expect(page.locator('.detail-body')).toBeVisible();
  }
  await page.getByRole('button', { name: '待跟进', exact: true }).click();
  await expect(page.getByRole('heading', { name: /待跟进/ })).toBeVisible();
  await page.getByRole('button', { name: '项目管道', exact: true }).click();
  await expect(page.locator('.pipeline')).toBeVisible();
  await page.getByRole('button', { name: '列表视图', exact: true }).click();
  await expect(page.getByRole('heading', { name: /我的报备/ })).toBeVisible();
  await page.getByRole('button', { name: '统计', exact: true }).click();
  await expect(page.getByRole('heading', { name: '基础统计', exact: true })).toBeVisible();
});
