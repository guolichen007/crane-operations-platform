// Q2 formal projects + initial project grants — real browser against a stateful mocked API that
// enforces the server rules (manager-only, expectedRole guard, valid users/roles, no self-grant).
// The same flows against the real BFF + PostgreSQL: portfolio_projects_http_test.go; against
// real Keycloak: tests/e2e/b4/portfolio-grants.spec.ts.
import { test, expect, type Page } from '@playwright/test';

type Member = { userId: string; displayName: string; role: string; enabled: boolean };
const shot = (name: string) => `test-results/q2-grants/${name}.png`;
const formal = (over: Record<string, unknown> = {}) => ({
  id: 'FP-1', name: 'Q2 华东新装项目', portfolioScopeId: 'PS-A', intakeId: 'I1', intakeCode: 'INT-Q2', projectPhase: 'REGISTERED',
  memberCount: 0, grantStatus: 'NO_MEMBERS', inventoryRegistered: false, adminCode: null, canManageGrants: true, ...over,
});

async function mock(page: Page, opts: { role?: 'portfolio_manager' | 'portfolio_planner'; fail?: Set<string>; delayMs?: number; projects?: unknown[] } = {}) {
  const role = opts.role ?? 'portfolio_manager';
  const fail = opts.fail ?? new Set<string>();
  const members: Member[] = [];
  const posts: unknown[] = [];
  const known = new Set(['user-new', 'user-two']);
  const session = { userId: 'user-pm', displayName: 'pm', projects: [] as unknown[], csrfToken: 'x'.repeat(32), expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' };
  await page.route('**/auth/session', (r) => r.fulfill({ json: session }));
  await page.route('**/api/v1/**', async (r) => {
    const p = new URL(r.request().url()).pathname.replace('/api/v1/', '');
    const m = r.request().method();
    if (fail.has(`${m} ${p}`)) return r.fulfill({ status: 503, json: { error: 'database unavailable' } });
    if (p === 'portfolio/scopes') return r.fulfill({ json: [{ id: 'PS-A', tenantId: 'demo', code: 'PS-A', name: '华东项目组合', status: 'ACTIVE', version: 1, myRole: role }] });
    if (p === 'portfolio/intakes') return r.fulfill({ json: [{ id: 'I1', portfolioScopeId: 'PS-A', tenantId: 'demo', intakeCode: 'INT-Q2', projectName: 'Q2 华东新装项目', customerName: null, projectType: 'NEW', priority: 'MEDIUM', riskLevel: 'MEDIUM', scopeSummary: '', siteReference: null, registeredAt: '2026-11-10T00:00:00Z', registeredBy: 'u', requestedStart: null, requestedHandover: null, plannedStart: null, plannedEnd: null, status: 'APPROVED', projectManagerUserId: null, technicalLeadUserId: null, siteLeadUserId: null, formalProjectId: 'FP-1', version: 3 }] });
    if (p === 'portfolio/projects') return r.fulfill({ json: opts.projects ?? [formal({ canManageGrants: role === 'portfolio_manager', memberCount: members.length, grantStatus: members.length ? 'MEMBERS_CONFIGURED' : 'NO_MEMBERS' })] });
    if (p === 'portfolio/projects/FP-1/members' && m === 'GET') return role === 'portfolio_manager' ? r.fulfill({ json: members }) : r.fulfill({ status: 403, json: { error: 'Forbidden' } });
    if (p === 'portfolio/projects/FP-1/members' && m === 'POST') {
      if (opts.delayMs) await new Promise((x) => setTimeout(x, opts.delayMs));
      const b = r.request().postDataJSON() as { userId: string; role: string | null; expectedRole: string | null };
      posts.push(b);
      if (role !== 'portfolio_manager') return r.fulfill({ status: 403, json: { error: 'Forbidden' } });
      if (!known.has(b.userId)) return r.fulfill({ status: 422, json: { error: 'grant target is not an enabled user of this identity provider' } });
      const cur = members.find((x) => x.userId === b.userId);
      if ((cur?.role ?? null) !== b.expectedRole) return r.fulfill({ status: 409, json: { error: 'project membership changed concurrently' } });
      if (b.role === null) members.splice(members.indexOf(cur!), 1);
      else if (cur) cur.role = b.role;
      else members.push({ userId: b.userId, displayName: 'Anonymous ' + b.userId, role: b.role, enabled: true });
      return r.fulfill({ json: members });
    }
    return r.fulfill({ json: [] });
  });
  return { members, posts, session };
}

async function openFormal(page: Page) {
  await page.goto('/#/portfolio');
  await page.getByTestId('tab-formal').click();
  await page.locator('[data-formal-id="FP-1"]').getByTestId('formal-open').click();
  return page.getByTestId('formal-panel');
}

test('经理：查看正式项目 → 授予 → 调整角色 → 撤销，状态与成员列表随之刷新', async ({ page }) => {
  const api = await mock(page);
  // Entry from the intake row (Q1 "后续环节") leads to the formal project.
  await page.goto('/#/portfolio');
  await page.locator('[data-intake-id="I1"]').getByTestId('open-formal').click();
  const panel = page.getByTestId('formal-panel');
  await expect(panel).toContainText('Q2 华东新装项目');
  await expect(panel.getByTestId('formal-grant-status')).toContainText('尚未配置');
  await expect(panel.getByTestId('members-empty')).toContainText('任何人');
  await expect(panel).toContainText('未进入运营地图');
  await page.screenshot({ path: shot('1-formal-no-members') });
  const form = panel.getByTestId('grant-form');
  await form.getByLabel('平台账号 ID').fill('user-new');
  await form.getByLabel('项目角色').selectOption('platform_admin');
  await expect(form.getByTestId('role-detail')).toContainText('不含设备控制');
  await form.getByTestId('grant-submit').click();
  await expect(panel.getByTestId('grant-notice')).toContainText('立即生效');
  await expect(panel.locator('[data-member="user-new"]')).toBeVisible();
  await expect(page.locator('[data-formal-id="FP-1"]')).toContainText('1 名成员');
  expect(api.posts.at(-1)).toEqual({ userId: 'user-new', role: 'platform_admin', expectedRole: null });
  await page.screenshot({ path: shot('2-granted') });
  page.once('dialog', (d) => void d.accept());
  await panel.getByLabel('user-new 的项目角色').selectOption('viewer');
  await expect(panel.getByTestId('grant-notice')).toContainText('只读');
  expect(api.posts.at(-1)).toEqual({ userId: 'user-new', role: 'viewer', expectedRole: 'platform_admin' });
  page.once('dialog', (d) => { expect(d.message()).toContain('下一次请求'); void d.accept(); });
  await panel.locator('[data-member="user-new"]').getByTestId('member-revoke').click();
  await expect(panel.getByTestId('members-empty')).toBeVisible();
  expect(api.posts.at(-1)).toEqual({ userId: 'user-new', role: null, expectedRole: 'viewer' });
});

test('错误提示：未知账号 422、并发修改 409、服务 503；失败保留输入，保存中防重复提交', async ({ page }) => {
  const fail = new Set<string>();
  const api = await mock(page, { fail, delayMs: 800 });
  const panel = await openFormal(page);
  const form = panel.getByTestId('grant-form');
  await form.getByTestId('grant-submit').click();
  await expect(form.locator('.field-error')).toHaveCount(2);
  expect(api.posts).toHaveLength(0);
  await form.getByLabel('平台账号 ID').fill('user-pm');
  await form.getByTestId('grant-submit').click();
  await expect(form.locator('.field-error').first()).toContainText('自己');
  await form.getByLabel('平台账号 ID').fill('nobody');
  await form.getByLabel('项目角色').selectOption('viewer');
  await form.getByTestId('grant-submit').click();
  await expect(form.getByRole('button', { name: '保存中…' })).toBeDisabled();
  await form.getByRole('button', { name: '保存中…' }).click({ force: true }).catch(() => {});
  await expect(panel.getByTestId('grant-error')).toContainText('已停用');
  expect(api.posts).toHaveLength(1);
  await expect(form.getByLabel('平台账号 ID')).toHaveValue('nobody');
  // Someone else granted user-two after our list was loaded: our request still carries
  // expectedRole=null, so the server answers 409 (no silent overwrite) and the list refreshes.
  api.members.push({ userId: 'user-two', displayName: 'two', role: 'operator', enabled: true });
  await form.getByLabel('平台账号 ID').fill('user-two');
  await form.getByTestId('grant-submit').click();
  await expect(panel.getByTestId('grant-error')).toContainText('已被他人修改');
  expect(api.posts.at(-1)).toEqual({ userId: 'user-two', role: 'viewer', expectedRole: null });
  await expect(panel.locator('[data-member="user-two"]')).toBeVisible();
  fail.add('POST portfolio/projects/FP-1/members');
  await form.getByLabel('平台账号 ID').fill('user-new');
  await form.getByTestId('grant-submit').click();
  await expect(panel.getByTestId('grant-error')).toContainText('已保留');
  await expect(form.getByLabel('平台账号 ID')).toHaveValue('user-new');
  await page.screenshot({ path: shot('3-errors') });
});

test('并发：成员已被他人改动时得到 409 并刷新列表', async ({ page }) => {
  const api = await mock(page);
  const panel = await openFormal(page);
  api.members.push({ userId: 'user-new', displayName: 'n', role: 'viewer', enabled: true });
  await panel.getByTestId('grant-form').getByLabel('平台账号 ID').fill('user-new');
  await panel.getByTestId('grant-form').getByLabel('项目角色').selectOption('operator');
  await panel.getByTestId('grant-submit').click();
  await expect(panel.getByTestId('grant-error')).toContainText('已被他人修改');
  await expect(panel.locator('[data-member="user-new"]')).toBeVisible();
});

test('规划者：可查看正式项目，没有授权入口；空状态与失败状态区分', async ({ page }) => {
  await mock(page, { role: 'portfolio_planner' });
  const panel = await openFormal(page);
  await expect(panel.getByTestId('grant-readonly')).toContainText('项目组合经理');
  await expect(panel.getByTestId('grant-form')).toHaveCount(0);
  await page.screenshot({ path: shot('4-planner-readonly') });

  const p2 = await page.context().newPage();
  await mock(p2, { projects: [] });
  await p2.goto('/#/portfolio');
  await p2.getByTestId('tab-formal').click();
  await expect(p2.getByTestId('formal-empty')).toBeVisible();

  const p3 = await page.context().newPage();
  await mock(p3, { fail: new Set(['GET portfolio/projects']) });
  await p3.goto('/#/portfolio');
  // Formal facts unavailable → intake stages say "unknown", never "not on the map".
  await expect(p3.locator('[data-intake-id="I1"]').getByTestId('intake-stages')).toContainText('运营地图状态未知');
  await expect(p3.locator('[data-intake-id="I1"]').getByTestId('intake-stages')).not.toContainText('未进入运营地图');
  await p3.getByTestId('tab-formal').click();
  await expect(p3.getByTestId('formal-error')).toContainText('服务暂不可用');
  await expect(p3.getByText('暂无已批准的正式项目')).toHaveCount(0);
  await p3.screenshot({ path: shot('5-formal-503') });
});

for (const [w, h] of [[1280, 720], [1366, 768], [1920, 1080]] as const) {
  test(`正式项目详情与授权表单在 ${w}×${h} 可读可用`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: h });
    const api = await mock(page);
    api.members.push({ userId: 'user-two', displayName: '张工（项目二组）', role: 'maintainer', enabled: true });
    const panel = await openFormal(page);
    await panel.getByTestId('grant-form').getByLabel('项目角色').selectOption('operator');
    for (const t of ['grant-submit', 'member-revoke']) {
      const el = panel.getByTestId(t).first();
      await el.scrollIntoViewIfNeeded();
      await expect(el).toBeInViewport();
      await expect(el).toBeEnabled();
    }
    await expect(panel.getByTestId('role-detail')).toBeVisible();
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    await panel.scrollIntoViewIfNeeded();
    await page.screenshot({ path: shot(`6-panel-${w}x${h}`), fullPage: false });
  });
}
