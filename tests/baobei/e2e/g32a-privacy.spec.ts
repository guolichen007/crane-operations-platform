import {
  test,
  expect,
  type Page,
} from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
// G3-2A: a related candidate in a team the reviewer cannot read is reported
// only by one fixed notice. No count, risk, score, candidate fields or action
// buttons for it; the director still sees and decides it.
const NOTICE = '检测到需要跨组核验的相关项目。当前账号无权查看详情，请销售总监处理。';
const action = (p: Page, id: string) => p.locator(`[data-action-id="${id}"]:visible`);
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
const doc = (name: string, customer: string, workshop: string) => ({
  projectName: name,
  customer,
  location: { province: '合成省', city: '合成市', factoryName: '合成一厂', workshop },
  projectType: 'RETROFIT',
  salesStage: 'L1_REQUIREMENT',
  informationSource: 'END_CUSTOMER',
  confidenceLevel: 'CUSTOMER_CONFIRMED',
  requirements: ['ANTI_SWAY'],
  requirementNotes: '合成需求',
  plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 2 }],
});
async function submit(p: Page, body: ReturnType<typeof doc>) {
  const r = await api(p, '/api/v1/projects', 'POST', body);
  expect(r.status).toBe(200);
  const s = await api(p, `/api/v1/projects/${r.body.id}/submit`, 'POST', { version: r.body.version });
  expect(s.status).toBe(200);
  return s.body;
}
test('G3-2A 跨组候选只显示固定提示，总监可完整处理', async ({ page }, info) => {
  const tag = 'G32A' + info.project.name + Date.now();
  const name = tag + '跨组隐私', customer = tag + '合成客户';
  // Hidden candidate: team two. Its workshop is unique so any leak is detectable.
  await login(page, 'synthetic-c');
  const hidden = await submit(page, doc(name, customer, '隐藏车间' + tag));
  await logout(page);
  await login(page, 'synthetic-b');
  const primary = await submit(page, doc(name, customer, '合成车间'));
  expect(primary.intakeStatus).toBe('UNDER_REVIEW');
  // Sales: the duplicate check reports a match without any detail.
  const check = await api(page, '/api/v1/duplicate-checks', 'POST', doc(name, customer, '合成车间'));
  expect(check.status).toBe(200);
  expect(check.body.restrictedRelated?.code).toBe('RESTRICTED_RELATED_PRESENT');
  expect(JSON.stringify(check.body)).not.toContain(hidden.id);
  await logout(page);

  await login(page, 'synthetic-manager');
  await page.goto('/duplicates');
  await action(page, 'review.open').filter({ hasText: primary.registrationNo }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByTestId('restricted-related')).toHaveText(NOTICE);
  await expect(dialog).not.toContainText(hidden.registrationNo);
  await expect(dialog).not.toContainText('隐藏车间');
  await expect(dialog).not.toContainText('高风险');
  await expect(dialog).not.toContainText('疑似重复 ·');
  await expect(dialog.locator('.comparison-grid')).toHaveCount(0);
  await expect(action(page, 'duplicate.confirm')).toHaveCount(0);
  await expect(action(page, 'duplicate.dismiss')).toHaveCount(0);
  await expect(action(page, 'review.approve')).toBeDisabled();
  // Direct POST is still rejected by the server.
  const cmp = await api(page, `/api/v1/projects/${primary.id}/duplicate-comparison`);
  expect(cmp.body.cases).toEqual([]);
  const post = await api(page, `/api/v1/projects/${primary.id}/approve`, 'POST', { version: primary.version, reason: '合成越权批准' });
  expect([403, 409]).toContain(post.status);
  // Detail page: fixed reason, no hidden count.
  await page.goto('/projects/' + primary.id);
  await action(page, 'project.tab').filter({ hasText: '审批和重复风险' }).click();
  await expect(page.getByTestId('restricted-related')).toContainText(NOTICE);
  await expect(page.locator('.detail-body')).not.toContainText('待裁决重复候选');
  await expect(page.locator('.detail-body')).not.toContainText(hidden.registrationNo);
  expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);
  await logout(page);

  // Director: full comparison and an actionable decision.
  await login(page, 'synthetic-director');
  await page.goto('/duplicates');
  await action(page, 'review.open').filter({ hasText: primary.registrationNo }).click();
  await expect(page.getByRole('dialog').getByTestId('restricted-related')).toHaveCount(0);
  await expect(page.getByRole('dialog').locator('.comparison-grid')).toContainText('隐藏车间' + tag);
  await action(page, 'duplicate.dismiss').first().click();
  await page.locator('[name=reviewReason]').fill('合成总监跨组核验');
  await action(page, 'review.confirm').click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  const done = await api(page, `/api/v1/projects/${primary.id}`);
  expect(done.body.intakeStatus).toBe('APPROVED');
});
