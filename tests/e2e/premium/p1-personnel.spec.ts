// P1 personnel / account management — real browser against a stateful mocked API that enforces the
// server rules (system capability, idempotent provisioning, one-time password, last-admin, versioned
// writes). The same rules against the real BFF + PostgreSQL: personnel_http_test.go /
// internal/personnel; against real Keycloak 26.8.1: tests/e2e/b4/personnel.spec.ts.
import { test, expect, type Page } from '@playwright/test';

type P = Record<string, unknown> & { id: string; username: string; displayName: string; enabled: boolean; systemRoles: string[]; version: number };
const shot = (name: string) => `test-results/p1-personnel/${name}.png`;

async function mock(page: Page, opts: { roles?: string[]; failProvisionOnce?: boolean } = {}) {
  const roles = opts.roles ?? ['system_admin'];
  const caps = {
    personnel_read: roles.length > 0, account_manage: roles.length > 0, personnel_audit_read: roles.length > 0,
    department_manage: roles.includes('system_admin'), system_role_manage: roles.includes('system_admin'), portfolio_role_manage: roles.includes('system_admin'),
  };
  const now = '2026-11-18T08:00:00Z';
  const person = (id: string, over: Partial<P> = {}): P => ({ id, username: id, displayName: '员工 ' + id, departmentId: 'ops', departmentName: '运维部', jobTitle: '工程师', phone: '', email: '',
    enabled: true, managed: true, identitySync: 'SYNCED', createdAt: now, createdBy: 'host:ops', lastLoginAt: null, version: 1, systemRoles: [], projectCount: 0, portfolioCount: 0, ...over });
  const people: P[] = [person('root-admin', { displayName: '系统管理员', systemRoles: ['system_admin'], lastLoginAt: now }), person('worker-1')];
  const portfolio: Record<string, Record<string, string>> = {};
  const provisions = new Map<string, { body: unknown; done: boolean }>();
  const calls: { method: string; path: string; body?: unknown; headers: Record<string, string> }[] = [];
  let failOnce = !!opts.failProvisionOnce;
  const session = { userId: 'root-admin', displayName: '系统管理员', projects: [], csrfToken: 'x'.repeat(32), expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' };
  await page.route('**/auth/session', (r) => r.fulfill({ json: session }));
  await page.route('**/api/v1/**', async (r) => {
    const url = new URL(r.request().url());
    const p = url.pathname.replace('/api/v1/', '');
    const m = r.request().method();
    const body = m === 'GET' ? undefined : r.request().postDataJSON();
    calls.push({ method: m, path: p, body, headers: r.request().headers() });
    if (p === 'admin/me') return r.fulfill({ json: { userId: 'root-admin', systemRoles: roles, capabilities: caps, accountServiceConfigured: true } });
    if (p.startsWith('admin/') && !roles.length) return r.fulfill({ status: 403, json: { error: 'Forbidden' } });
    if (p === 'admin/departments') return r.fulfill({ json: [{ id: 'ops', name: '运维部', status: 'ACTIVE', version: 1, memberCount: 2 }] });
    if (p === 'admin/portfolio-scopes') return r.fulfill({ json: [{ id: 'PS-A', name: '华东项目组合', status: 'ACTIVE' }] });
    if (p === 'admin/provisions') return r.fulfill({ json: [] });
    if (p === 'admin/people' && m === 'GET') {
      const q = url.searchParams.get('q') ?? '';
      const st = url.searchParams.get('status') ?? '';
      const items = people.filter((x) => (!q || x.displayName.includes(q) || x.username.includes(q)) && (!st || (st === 'ENABLED' ? x.enabled : !x.enabled)));
      return r.fulfill({ json: { items, total: items.length } });
    }
    if (p === 'admin/people' && m === 'POST') {
      const key = r.request().headers()['idempotency-key'];
      const b = body as { username: string; profile: { displayName: string }; systemRoles: string[]; portfolioGrants: { scopeId: string; role: string }[] };
      if (!key) return r.fulfill({ status: 422, json: { error: 'missing key' } });
      const prior = provisions.get(key);
      if (prior?.done) return r.fulfill({ json: { provisionId: 'prov-' + key, state: 'COMPLETED', person: people.find((x) => x.id === b.username), passwordDelivered: false, replayed: true } });
      if (!prior && people.some((x) => x.username === b.username)) return r.fulfill({ status: 409, json: { error: 'username already exists' } });
      provisions.set(key, { body: b, done: false });
      if (failOnce) {
        failOnce = false;
        return r.fulfill({ status: 503, json: { error: 'identity provider unavailable', code: 'IDENTITY_PROVIDER' } });
      }
      provisions.get(key)!.done = true;
      const np = person(b.username, { displayName: b.profile.displayName, systemRoles: b.systemRoles, portfolioCount: b.portfolioGrants.length });
      people.push(np);
      portfolio[np.id] = Object.fromEntries(b.portfolioGrants.map((g) => [g.scopeId, g.role]));
      return r.fulfill({ status: 201, json: { provisionId: 'prov-' + key, state: 'COMPLETED', person: np, temporaryPassword: 'AAAAAAAAAAAAAAA1', passwordDelivered: true, replayed: false } });
    }
    const one = p.match(/^admin\/people\/([^/]+)(?:\/(.+))?$/);
    if (one) {
      const target = people.find((x) => x.id === decodeURIComponent(one[1]));
      if (!target) return r.fulfill({ status: 404, json: { error: 'not found' } });
      const action = one[2];
      const ifMatch = Number((r.request().headers()['if-match'] ?? '').replaceAll('"', ''));
      const detail = () => ({ ...target, portfolioGrants: Object.entries(portfolio[target.id] ?? {}).map(([scopeId, role]) => ({ scopeId, scopeName: '华东项目组合', role })), projectGrants: [],
        identity: { available: true, exists: true, enabled: target.enabled, passwordChangeRequired: true }, history: [{ at: now, actor: 'root-admin', action: 'personnel_created', result: 'success', scope: 'account', change: { username: target.username }, requestId: 'r1', sourceIp: '' }] });
      if (!action && m === 'GET') return r.fulfill({ json: detail() });
      if (!action && m === 'PATCH') {
        if (ifMatch !== target.version) return r.fulfill({ status: 409, json: { error: 'the person was changed by someone else' } });
        Object.assign(target, body as object, { version: target.version + 1 });
        return r.fulfill({ json: { person: target, identitySync: 'SYNCED', sessionsRevoked: 0 } });
      }
      if (action === 'disable' || action === 'enable') {
        if (target.id === 'root-admin') return r.fulfill({ status: 422, json: { error: 'self', code: 'SELF' } });
        if (ifMatch !== target.version) return r.fulfill({ status: 409, json: { error: 'stale' } });
        target.enabled = action === 'enable';
        target.version++;
        return r.fulfill({ json: { person: target, identitySync: 'SYNCED', sessionsRevoked: action === 'disable' ? 2 : 0 } });
      }
      if (action === 'reset-password') return r.fulfill({ json: { person: target, identitySync: 'SYNCED', sessionsRevoked: 1, temporaryPassword: 'BBBBBBBBBBBBBBB2' } });
      if (action === 'revoke-sessions') return r.fulfill({ json: { person: target, identitySync: 'SYNCED', sessionsRevoked: 3 } });
      if (action === 'system-roles') {
        const b = body as { roles: string[]; expectedRoles: string[] };
        if (target.id === 'root-admin') return r.fulfill({ status: 422, json: { error: 'self', code: 'SELF' } });
        if (b.expectedRoles.join() !== target.systemRoles.join()) return r.fulfill({ status: 409, json: { error: 'changed' } });
        target.systemRoles = b.roles;
        return r.fulfill({ json: target });
      }
      const pr = action?.match(/^portfolio-roles\/(.+)$/);
      if (pr) {
        const b = body as { role: string | null; expectedRole: string | null };
        const cur = portfolio[target.id]?.[pr[1]] ?? null;
        if ((cur ?? null) !== (b.expectedRole || null)) return r.fulfill({ status: 409, json: { error: 'changed' } });
        portfolio[target.id] ??= {};
        if (b.role) portfolio[target.id][pr[1]] = b.role;
        else delete portfolio[target.id][pr[1]];
        target.portfolioCount = Object.keys(portfolio[target.id]).length;
        return r.fulfill({ json: target });
      }
    }
    return r.fulfill({ json: [] });
  });
  return { people, calls };
}

test('系统管理员：三步开户 → 一次性初始密码 → 人员详情；重复提交复用幂等键', async ({ page }) => {
  const api = await mock(page, { failProvisionOnce: true });
  await page.goto('/#/overview');
  await page.getByTestId('rail-system').click();
  await expect(page.getByTestId('people-table')).toContainText('系统管理员');
  await page.screenshot({ path: shot('1-list') });
  await page.getByTestId('person-create').click();
  const wiz = page.getByTestId('person-wizard');
  await wiz.getByTestId('wiz-next').click();
  await expect(wiz.locator('.field-error')).toHaveCount(2);
  await wiz.getByTestId('wiz-name').fill('张三');
  await wiz.getByTestId('wiz-username').fill('Zhang San');
  await wiz.getByTestId('wiz-next').click();
  await expect(wiz.locator('.field-error')).toContainText('小写');
  await wiz.getByTestId('wiz-username').fill('zhangsan');
  await wiz.getByTestId('wiz-department').selectOption('ops');
  await wiz.getByTestId('wiz-job').fill('调度员');
  await wiz.getByTestId('wiz-next').click();
  await wiz.getByTestId('wiz-add-scope').click();
  await wiz.getByTestId('wiz-scope-0').selectOption('PS-A');
  await wiz.getByTestId('wiz-scope-role-0').selectOption('portfolio_planner');
  await page.screenshot({ path: shot('2-wizard-permissions') });
  await wiz.getByTestId('wiz-next').click();
  await expect(wiz.getByTestId('wiz-summary')).toContainText('华东项目组合：项目组合规划者');
  await wiz.getByTestId('wiz-next').click();
  // Identity provider outage: error shown, draft kept, retry with the SAME key completes.
  await expect(wiz.getByTestId('wiz-error')).toContainText('可稍后重试');
  await wiz.getByRole('button', { name: '重试开通' }).click();
  const otp = page.getByTestId('one-time-password');
  await expect(otp.getByTestId('otp-username')).toHaveText('zhangsan');
  await expect(otp.getByTestId('otp-password')).toHaveText('••••••••••••••••');
  await otp.getByTestId('otp-reveal').click();
  await expect(otp.getByTestId('otp-password')).toHaveText('AAAAAAAAAAAAAAA1');
  await expect(otp.getByTestId('otp-close')).toBeDisabled();
  await page.screenshot({ path: shot('3-one-time-password') });
  const posts = api.calls.filter((c) => c.method === 'POST' && c.path === 'admin/people');
  expect(posts).toHaveLength(2);
  expect(posts[0].headers['idempotency-key']).toBe(posts[1].headers['idempotency-key']);
  // The password is never written to Web Storage.
  const stored = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }));
  expect(stored).not.toContain('AAAAAAAAAAAAAAA1');
  await otp.getByTestId('otp-confirm').check();
  await otp.getByTestId('otp-close').click();
  await expect(page.getByTestId('person-detail')).toBeVisible();
  await expect(page.getByTestId('person-username')).toHaveText('zhangsan');
  await expect(page.getByTestId('detail-portfolio')).toContainText('华东项目组合');
  // After closing, the password is gone from the page.
  await expect(page.locator('body')).not.toContainText('AAAAAAAAAAAAAAA1');
});

test('人员详情：停用/启用（If-Match）、重置密码只显示一次、撤销会话、系统角色与项目组合角色', async ({ page }) => {
  const api = await mock(page);
  await page.goto('/#/system/people/worker-1');
  const d = page.getByTestId('person-detail');
  await expect(d.getByTestId('person-status')).toHaveText('启用');
  page.on('dialog', (x) => void x.accept());
  await d.getByTestId('person-disable').click();
  await expect(d.getByTestId('person-notice')).toContainText('已结束 2 个在线会话');
  await expect(d.getByTestId('person-status')).toHaveText('已停用');
  expect(api.calls.find((c) => c.path === 'admin/people/worker-1/disable')?.headers['if-match']).toBe('"1"');
  await d.getByTestId('person-enable').click();
  await expect(d.getByTestId('person-notice')).toContainText('不会恢复');
  await d.getByTestId('person-reset').click();
  const otp = page.getByTestId('one-time-password');
  await otp.getByTestId('otp-reveal').click();
  await expect(otp.getByTestId('otp-password')).toHaveText('BBBBBBBBBBBBBBB2');
  await otp.getByTestId('otp-confirm').check();
  await otp.getByTestId('otp-close').click();
  await d.getByTestId('person-revoke').click();
  await expect(d.getByTestId('person-notice')).toContainText('已撤销 3 个');
  await d.getByTestId('detail-role-account_manager').check();
  await d.getByTestId('detail-roles-save').click();
  await expect(d.getByTestId('person-notice')).toContainText('系统角色已更新');
  expect(api.calls.find((c) => c.path.endsWith('system-roles'))?.body).toEqual({ roles: ['account_manager'], expectedRoles: [] });
  await d.getByTestId('detail-scope').selectOption('PS-A');
  await d.getByTestId('detail-scope-role').selectOption('portfolio_manager');
  await d.getByTestId('detail-scope-add').click();
  await expect(d.getByTestId('detail-portfolio')).toContainText('华东项目组合');
  await expect(d.getByTestId('person-history')).toContainText('开户');
  await page.screenshot({ path: shot('4-detail'), fullPage: true });
  // Self: no disable, no self role change.
  await page.evaluate(() => { location.hash = '#/system/people/root-admin'; });
  await expect(page.getByTestId('person-disable')).toBeDisabled();
  await expect(page.getByTestId('detail-roles-save')).toHaveCount(0);
});

test('无系统角色：侧栏无入口，直接访问提示无权限且不发管理请求', async ({ page }) => {
  const api = await mock(page, { roles: [] });
  await page.goto('/#/overview');
  await expect(page.getByTestId('side-rail')).toBeVisible();
  await expect(page.getByTestId('rail-system')).toHaveCount(0);
  await page.getByTestId('side-rail').evaluate(() => { location.hash = '#/system/people'; });
  await expect(page.getByTestId('personnel-forbidden')).toBeVisible();
  expect(api.calls.filter((c) => c.path.startsWith('admin/') && c.path !== 'admin/me')).toHaveLength(0);
});

for (const [w, h] of [[375, 812], [1280, 720], [1920, 1080]] as const) {
  test(`人员与账号在 ${w}×${h} 可读可用、无横向溢出`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: h });
    await mock(page);
    await page.goto('/#/system/people');
    await expect(page.getByTestId('people-table')).toBeVisible();
    await page.getByTestId('person-create').click();
    const wiz = page.getByTestId('person-wizard');
    for (const id of ['wiz-name', 'wiz-username', 'wiz-next']) {
      const el = wiz.getByTestId(id);
      await el.scrollIntoViewIfNeeded();
      await expect(el).toBeInViewport();
    }
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    await page.screenshot({ path: shot(`5-wizard-${w}x${h}`) });
    await wiz.getByRole('button', { name: '取消' }).click();
    await page.evaluate(() => { location.hash = '#/system/people/worker-1'; });
    await expect(page.getByTestId('person-reset')).toBeVisible();
    const overflow2 = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow2).toBeLessThanOrEqual(0);
    await page.screenshot({ path: shot(`6-detail-${w}x${h}`), fullPage: true });
  });
}
