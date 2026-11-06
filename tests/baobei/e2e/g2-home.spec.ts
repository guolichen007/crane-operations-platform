import {
  test,
  expect,
  type Page,
} from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
// G2-4: task home counts are server aggregates over the full scope, not the
// first page. Each tile opens the list with the same filter and total.
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
const tile = (p: Page, label: string) => action(p, 'dashboard.task').filter({ hasText: label });

test('home counts equal server totals beyond 100 records and tiles open the same list', async ({ page }, info) => {
  // synthetic-staff-* accounts are suspended by staff.spec; use a sales account no suite changes.
  await login(page, 'synthetic-c');
  const tag = `G2首页-${info.project.name}-${Date.now()}`;
  // 105 drafts so that any "first 100 records" computation would be wrong.
  for (let i = 0; i < 105; i++) expect((await api(page, '/api/v1/projects', 'POST', { projectName: `${tag}-${i}`, version: 1 })).status).toBe(200);
  await page.reload();
  const summary = (await api(page, '/api/v1/workspace-summary')).body;
  expect(summary.counts.drafts).toBeGreaterThanOrEqual(105);
  await expect(tile(page, '草稿').locator('.task-count')).toHaveText(String(summary.counts.drafts));
  await expect(page.locator('.scope-line')).toContainText('共 ' + summary.counts.total + ' 项');
  await expect(page.locator('.scope-line')).toContainText('统计时间');
  const list = (await api(page, '/api/v1/projects?status=DRAFT&pageSize=1')).body;
  expect(list.total).toBe(summary.counts.drafts);
  await tile(page, '草稿').click();
  await expect(page).toHaveURL(/\/projects\?status=DRAFT/);
  await expect(page.locator('h1 small')).toHaveText(String(summary.counts.drafts));
});

test('admin home: zero data, unassigned draft and reviewable count follow independence', async ({ page }, info) => {
  await login(page, 'synthetic-admin');
  const before = (await api(page, '/api/v1/workspace-summary')).body.counts;
  await expect(tile(page, '待分配草稿').locator('.task-count')).toHaveText(String(before.unassignedDrafts));
  const name = `G2首页管理员-${info.project.name}-${Date.now()}`;
  const draft = (await api(page, '/api/v1/projects', 'POST', {
    projectName: name, customer: name, location: { province: '合成省', city: '合成市' }, projectType: 'RETROFIT',
    informationSource: 'END_CUSTOMER', confidenceLevel: 'CUSTOMER_CONFIRMED', requirements: ['ANTI_SWAY'], requirementNotes: '合成', version: 1,
  })).body;
  await page.reload();
  await expect(tile(page, '待分配草稿').locator('.task-count')).toHaveText(String(before.unassignedDrafts + 1));
  // Assign and submit as admin: the queue grows but "你可独立审核" does not.
  const assigned = (await api(page, `/api/v1/projects/${draft.id}/ownership-transfer`, 'POST', { version: draft.version, reason: '合成指派', targetSubject: 'synthetic-b' })).body;
  expect((await api(page, `/api/v1/projects/${draft.id}/submit`, 'POST', { version: assigned.version })).status).toBe(200);
  await page.reload();
  const after = (await api(page, '/api/v1/workspace-summary')).body.counts;
  expect(after.reviewQueue).toBe(before.reviewQueue + 1);
  expect(after.reviewable).toBe(before.reviewable);
  await expect(tile(page, '你可独立审核').locator('.task-count')).toHaveText(String(after.reviewable));
  await expect(page.getByText(/你参与过或证据待核验/)).toBeVisible();
});

test('home distinguishes empty scope, network failure and no permission', async ({ page }) => {
  await login(page, 'synthetic-c');
  const s = (await api(page, '/api/v1/workspace-summary')).body;
  if (s.counts.total === 0) {
    await expect(page.getByRole('heading', { name: '还没有报备' })).toBeVisible();
    await expect(action(page, 'project.create').last()).toBeVisible();
  }
  await page.route('**/api/v1/workspace-summary', (r) => r.abort('failed'));
  await page.reload();
  await expect(page.getByRole('alert')).toContainText('网络连接失败');
  await page.unroute('**/api/v1/workspace-summary');
  await action(page, 'dashboard.reload').click();
  await expect(page.locator('.task-board')).toBeVisible();
  // Sales never sees review tiles even if they guess the route.
  await expect(tile(page, '你可独立审核')).toHaveCount(0);
});
