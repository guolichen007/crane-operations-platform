import { fileURLToPath } from 'node:url';
import {
  test,
  expect,
  type Page,
} from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
const action = (p: Page, id: string) => p.locator(`[data-action-id="${id}"]:visible`);
async function login(p: Page, subject: string) {
  await p.goto('/login');
  await p.locator('select').selectOption(subject);
  const [response] = await Promise.all([
    p.waitForResponse((r) => new URL(r.url()).pathname === '/auth/dev' && r.request().method() === 'POST'),
    action(p, 'auth.dev-login').click(),
  ]);
  expect(response.status()).toBe(200);
  await expect(p.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
}
async function api(p: Page, path: string, method = 'GET', body?: unknown) {
  return p.evaluate(
    async ({ path, method, body }) => {
      const me = await (await fetch('/api/v1/me')).json();
      const r = await fetch(path, {
        method,
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': me.csrfToken },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      return { status: r.status, body: await r.json() };
    },
    { path, method, body }
  );
}
async function create(p: Page, name: string, customer: string) {
  const r = await api(p, '/api/v1/projects', 'POST', {
    projectName: name,
    customer,
    location: { province: '合成省', city: '合成市', factoryName: '合成一厂', workshop: '合成车间' },
    projectType: 'RETROFIT',
    salesStage: 'L1_REQUIREMENT',
    informationSource: 'END_CUSTOMER',
    confidenceLevel: 'CUSTOMER_CONFIRMED',
    requirements: ['ANTI_SWAY'],
    requirementNotes: 'Synthetic 需求',
    plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 2 }],
  });
  expect(r.status).toBe(200);
  const s = await api(p, `/api/v1/projects/${r.body.id}/submit`, 'POST', {
    version: r.body.version,
  });
  expect(s.status).toBe(200);
  return s.body;
}
async function confirm(p: Page) {
  await p.locator('[name=reviewReason]').fill('Synthetic主管逐项核验');
  await action(p, 'review.confirm').click();
  await expect(p.getByRole('dialog')).toHaveCount(0);
}
test('B2 manager approve return reject duplicate comparison and transfer', async ({
  page,
}, info) => {
  const prefix = 'SyntheticB2-' + info.project.name + '-' + Date.now();
  await login(page, 'synthetic-a');
  const approve = await create(page, prefix + '批准', prefix + '客户A'),
    returned = await create(page, prefix + '退回', prefix + '客户B'),
    rejected = await create(page, prefix + '拒绝', prefix + '客户C');
  const base = await create(page, prefix + '重复', prefix + '重复客户');
  const duplicate = await create(page, prefix + '重复', prefix + '重复客户');
  const distinct = await create(page, prefix + '重复', prefix + '重复客户');
  void base;
  await action(page, 'auth.logout').click();
  await expect(page).toHaveURL('/login');
  await login(page, 'synthetic-manager');
  await page.getByRole('button', { name: '报备审核', exact: true }).click();
  for (const [p, op] of [
    [approve, 'review.approve'],
    [returned, 'review.return'],
    [rejected, 'review.reject'],
  ] as const) {
    await action(page, 'review.open').filter({ hasText: p.projectName }).click();
    await action(page, op).click();
    await confirm(page);
  }
  await page.getByRole('button', { name: '重复处理', exact: true }).click();
  await action(page, 'review.open').filter({ hasText: duplicate.registrationNo }).click();
  await expect(page.locator('.comparison-grid')).toBeVisible();
  await expect(page.locator('.comparison-grid')).toContainText('合成车间');
  await page.screenshot({
    path: fileURLToPath(
      new URL(
        `../../../local-private/baobei/${info.project.name}-manager-comparison.png`,
        import.meta.url
      )
    ),
    fullPage: true,
  });
  await action(page, 'duplicate.confirm').first().click();
  await confirm(page);
  await action(page, 'review.open').filter({ hasText: distinct.registrationNo }).click();
  await action(page, 'duplicate.dismiss').first().click();
  await confirm(page);
  await page.goto('/projects/' + approve.id);
  await action(page, 'ownership.transfer').click();
  await page.locator('[name=targetOwner]').selectOption('synthetic-b');
  await page.locator('[name=governanceReason]').fill('Synthetic主管转移归属');
  await action(page, 'governance.confirm').click();
  await action(page, 'project.tab').filter({ hasText: '审计历史' }).click();
  await expect(page.locator('.detail-body')).toContainText('Synthetic主管转移归属');
  expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);
  await action(page, 'auth.logout').click();
  await expect(page).toHaveURL('/login');
  await login(page, 'synthetic-b');
  await page.goto('/projects/' + approve.id);
  await action(page, 'project.stage-change').click();
  await page.locator('[name=targetStage]').selectOption('L2_TECHNICAL');
  await page.locator('[name=governanceReason]').fill('Synthetic技术确认');
  await action(page, 'governance.confirm').click();
  await expect(page.locator('.detail-heading')).toContainText('技术交流');
});
for (const failure of ['html502', 'empty504', 'connection', 'timeout'])
  test('B2 API ' + failure + ' retains input', async ({ page }) => {
    await login(page, 'synthetic-a');
    await action(page, 'project.create').first().click();
    await page.locator('[name=customer]').fill('Synthetic网络故障保留');
    let held: Promise<void> | undefined;
    await page.route('**/api/v1/projects', async (route) => {
      if (route.request().method() !== 'POST') {
        await route.continue();
        return;
      }
      if (failure === 'html502')
        await route.fulfill({
          status: 502,
          contentType: 'text/html',
          body: '<html>bad gateway</html>',
        });
      else if (failure === 'empty504') await route.fulfill({ status: 504, body: '' });
      else if (failure === 'connection') await route.abort('failed');
      else {
        held = new Promise<void>((resolve) => setTimeout(resolve, 21000));
        await held;
        try {
          await route.abort('timedout');
        } catch {
          /* Client already aborted on timeout. */
        }
      }
    });
    await action(page, 'project.save-draft').click();
    const message =
      failure === 'html502'
        ? '服务暂时不可用'
        : failure === 'empty504'
          ? '服务响应超时'
          : failure === 'connection'
            ? '网络连接失败'
            : '请求超时';
    await expect(page.getByRole('alert')).toContainText(message, { timeout: 25000 });
    await expect(page.locator('[name=customer]')).toHaveValue('Synthetic网络故障保留');
    await expect(action(page, 'project.save-draft')).toBeEnabled();
    await page.getByRole('button', { name: '我的报备', exact: true }).click({ noWaitAfter: true });
    await action(page, 'guard.leave').click();
    if (held) await held;
  });
