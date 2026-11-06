import {
  test,
  expect,
  type Page,
} from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
// G2-3: project center, six-zone detail, admin actions and server follow-up windows.
const action = (p: Page, id: string) => p.locator(`[data-action-id="${id}"]:visible`);
async function login(p: Page, subject: string) {
  await p.goto('/login');
  await p.locator('select').selectOption(subject);
  await action(p, 'auth.dev-login').click();
  await expect(p.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
}
async function api(p: Page, path: string, method = 'GET', body?: unknown) {
  return p.evaluate(
    async ({ path, method, body }) => {
      const me = await (await fetch('/api/v1/me')).json();
      const r = await fetch(path, {
        method,
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': me.csrfToken, 'Idempotency-Key': 'e2e-' + crypto.randomUUID() },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      return { status: r.status, body: await r.json() };
    },
    { path, method, body }
  );
}
const complete = (name: string, extra: Record<string, unknown> = {}) => ({
  projectName: name,
  customer: name + '客户',
  location: { province: '合成省', city: '合成市', factoryName: '合成一厂' },
  projectType: 'RETROFIT',
  informationSource: 'END_CUSTOMER',
  confidenceLevel: 'CUSTOMER_CONFIRMED',
  requirements: ['ANTI_SWAY'],
  requirementNotes: '合成需求',
  plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 1 }],
  version: 1,
  ...extra,
});

test('admin assigns and submits from detail; independent manager approves from detail', async ({ page }, info) => {
  const name = `G2详情-${info.project.name}-${Date.now()}`;
  await login(page, 'synthetic-admin');
  const draft = await api(page, '/api/v1/projects', 'POST', complete(name));
  expect(draft.body.assignmentStatus).toBe('UNASSIGNED_DRAFT');
  await page.getByRole('button', { name: '待分配草稿', exact: true }).click();
  await page.getByRole('textbox', { name: '搜索报备' }).fill(name);
  await expect(page.locator('h1 small')).toHaveText('1');
  await action(page, 'project.preview').first().click();
  await action(page, 'project.detail').click();
  await expect(page.locator('.owner-tag')).toContainText('待分配');
  await expect(action(page, 'project.submit')).toBeDisabled();
  await action(page, 'ownership.transfer').click();
  await page.locator('[name=targetOwner]').selectOption({ label: '示例销售 A' });
  await page.locator('[name=governanceReason]').fill('合成管理员指派');
  await action(page, 'governance.confirm').click();
  await expect(page.locator('.owner-tag')).toContainText('示例销售 A');
  await action(page, 'project.tab').filter({ hasText: '归属历史' }).click();
  await expect(page.locator('.detail-body')).toContainText('待分配 → 示例销售 A');
  await expect(page.locator('.detail-body')).toContainText('示例管理员');
  await action(page, 'project.submit').click();
  await expect(page.locator('.detail-heading')).toContainText(/已提交|重复待审核/);
  await action(page, 'project.tab').filter({ hasText: '审批和重复风险' }).click();
  await expect(page.locator('.review-eligibility')).toContainText('你参与过此项目');
  await expect(action(page, 'review.approve')).toBeDisabled();
  await action(page, 'project.tab').filter({ hasText: '操作审计' }).click();
  await expect(page.locator('.detail-body')).toContainText('创建待分配草稿');
  await expect(page.locator('.detail-body')).not.toContainText('synthetic-admin');
  const id = page.url().split('/').pop()!;
  await action(page, 'auth.logout').click();
  // Wait for the logout to finish: /login redirects to / while the session is still valid.
  await expect(page).toHaveURL('/login');

  await login(page, 'synthetic-manager');
  await page.goto('/projects/' + id);
  await action(page, 'project.tab').filter({ hasText: '审批和重复风险' }).click();
  await expect(page.locator('.review-eligibility')).toContainText('你可以独立');
  await action(page, 'review.approve').click();
  await page.locator('[name=governanceReason]').fill('合成独立主管核验');
  await action(page, 'governance.confirm').click();
  await expect(page.locator('.detail-heading')).toContainText('已批准');
  const r = await api(page, '/api/v1/projects/' + id);
  expect(r.body.intakeStatus).toBe('APPROVED');
  expect(r.body.governanceHistory.some((g: { actorName: string; action: string }) => g.action === 'APPROVE' && g.actorName === '示例主管')).toBe(true);
});

test('follow-up windows are computed by the server and do not overlap', async ({ page }, info) => {
  const tag = `G2跟进-${info.project.name}-${Date.now()}`;
  await login(page, 'synthetic-b');
  const make = async (suffix: string, at: string) => {
    const p = await api(page, '/api/v1/projects', 'POST', complete(tag + suffix));
    const s = await api(page, `/api/v1/projects/${p.body.id}/submit`, 'POST', { version: p.body.version });
    const a = await api(page, `/api/v1/projects/${p.body.id}/activities`, 'POST', {
      version: s.body.version, type: 'PHONE', occurredAt: new Date().toISOString(), summary: '合成', result: '', nextAction: '合成下一步' + suffix, nextFollowupAt: at,
    });
    expect(a.status).toBe(200);
  };
  const day = 86400000;
  await make('-逾期', new Date(Date.now() - day).toISOString());
  await make('-三天', new Date(Date.now() + 3 * day).toISOString());
  await make('-十天', new Date(Date.now() + 10 * day).toISOString());
  const seen: string[] = [];
  page.on('request', (r) => r.url().includes('/api/v1/projects?') && seen.push(new URL(r.url()).search));
  await page.getByRole('button', { name: '待跟进', exact: true }).click();
  await page.getByRole('textbox', { name: '搜索报备' }).fill(tag);
  await expect(page.locator('h1 small')).toHaveText('1');
  await expect(page.locator(info.project.name === 'mobile-390' ? '.mobile-projects' : '.table-wrap')).toContainText('-逾期');
  await action(page, 'followup.window').filter({ hasText: '未来 7 天' }).click();
  await expect(page.locator('h1 small')).toHaveText('1');
  await expect(page.locator(info.project.name === 'mobile-390' ? '.mobile-projects' : '.table-wrap')).toContainText('-三天');
  expect(seen.some((q) => q.includes('followup=overdue'))).toBe(true);
  expect(seen.some((q) => q.includes('followup=upcoming'))).toBe(true);
  const bad = await api(page, '/api/v1/projects?followup=soon');
  expect(bad.status).toBe(400);
});

test('sales cannot reach admin or review routes; director sees no unassigned drafts', async ({ page }) => {
  await login(page, 'synthetic-a');
  await expect(page.getByRole('button', { name: '待分配草稿' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: '报备审核' })).toHaveCount(0);
  for (const path of ['/unassigned', '/reviews', '/settings/users']) {
    await page.goto(path);
    await expect(page).toHaveURL('/');
  }
  expect((await api(page, '/api/v1/reviews')).status).toBe(403);
  await action(page, 'auth.logout').click();
  // Wait for the logout to finish: /login redirects to / while the session is still valid.
  await expect(page).toHaveURL('/login');
  await login(page, 'synthetic-director');
  await expect(page.getByRole('button', { name: '待分配草稿' })).toHaveCount(0);
  const r = await api(page, '/api/v1/projects?assignment=unassigned');
  expect(r.body.total).toBe(0);
});
