import { type Page } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
import { test, expect, action, uniq, api } from './support/isolation';

// G5-C-U1: promotion of a member who already has a login, closed in the page.
// HTTP 200 of the promotion only means "submitted"; the administrator then syncs
// the exact login role ("同步登录角色"), and the change is in effect only after
// the member's own next login. Development runs the in-memory account service
// (same contract as the Keycloak adapter; real Keycloak: TestU1RealKeycloakRoleLoop).
const shots = process.env.BAOBEI_EVIDENCE_DIR;
async function shot(page: Page, name: string) {
  if (shots) await page.screenshot({ path: `${shots}/${name}.png`, fullPage: true });
}
async function login(page: Page, subject: string) {
  await page.goto('/login');
  await page.locator('select').selectOption(subject);
  await action(page, 'auth.dev-login').click();
  await expect(page.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
}
const phoneFor = (seed: string, slot: number) => {
  let h = slot * 104729;
  for (const ch of seed) h = (h * 37 + ch.charCodeAt(0)) % 100_000_000;
  return '134' + String(h).padStart(8, '0');
};
async function employeeSignIn(page: Page, phone: string) {
  return page.evaluate(async (phone) => {
    const r = await fetch('/auth/dev-phone', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ phone }) });
    return r.status;
  }, phone);
}

test('晋升主管：已提交 → 待身份处理（同步登录角色）→ 待本人重新登录 → 正式生效', async ({ page, browser, baseURL }, info) => {
  const mobile = info.project.name !== 'desktop';
  const unique = uniq('U1');
  const phone = phoneFor(unique, mobile ? 2 : 1);
  await login(page, 'synthetic-admin');
  await page.goto('/settings/users');
  await action(page, 'staff.invite').click();
  const add = page.getByRole('dialog', { name: '添加成员' });
  await add.getByLabel(/^姓名/).fill('合成晋升' + unique.slice(-5));
  await add.getByLabel(/^工作手机/).fill(phone);
  await add.getByLabel(/^团队/).selectOption({ index: 1 });
  await add.getByRole('button', { name: '添加成员' }).click();
  await expect(page.locator('.staff-title')).toContainText('待开通');
  const id = new URL(page.url()).pathname.split('/').pop()!;
  await action(page, 'staff.account-create').click();
  await page.getByRole('dialog', { name: '开通登录账号' }).getByRole('button', { name: '开通账号' }).click();
  await action(page, 'staff.credential-done').click();

  // Not yet identity-verified (never signed in): promotion refused by the server.
  const member = async () => (await api(page, '/api/v1/staff/' + id)).body.member;
  let m = await member();
  const early = await api(page, '/api/v1/staff/' + id, 'PATCH', { name: m.name, phone: m.phone, email: m.email, teamId: m.teamId,
    note: m.note, role: 'baobei_manager', reason: '未核验晋升', version: m.version });
  expect(early.status).toBe(409);
  expect(early.body.code).toBe('STAFF_IDENTITY_NOT_VERIFIED');

  // The member signs in once: ACTIVE sales.
  const ctx = await browser.newContext({ baseURL });
  const emp = await ctx.newPage();
  await emp.goto('/login');
  expect(await employeeSignIn(emp, phone)).toBe(200);
  await page.reload();
  await expect(page.locator('.staff-title')).toContainText('已启用');

  // Promote: 200 = submitted only.
  await action(page, 'staff.promote').click();
  const promote = page.getByRole('dialog', { name: '设为销售主管' });
  await promote.getByLabel(/^操作原因/).fill('业绩达标晋升');
  await promote.getByRole('button', { name: '保存' }).click();
  await expect(promote).toHaveCount(0);
  await expect(page.getByText('已提交，尚未生效', { exact: false }).first()).toBeVisible();
  await expect(page.locator('.staff-title')).toContainText('待身份处理');
  await expect(page.locator('.staff-title')).not.toContainText('已启用');
  await expect(page.getByTestId('role-progress')).toContainText('待身份处理');
  await expect(page.locator('.staff-hint').first()).toContainText('同步登录角色');
  await shot(page, (mobile ? 'm-' : 'd-') + 'u1-01-submitted');
  // The old sales session and the old login are gone.
  expect((await emp.request.get('/api/v1/projects')).status()).toBe(401);
  await emp.goto('/login');
  expect(await employeeSignIn(emp, phone)).toBe(403);
  // The list says the same.
  m = await member();
  expect(m.status).toBe('PENDING_PROVISION');

  // Role sync. A manager role needs a fresh password check: a stale check is
  // refused by the server and the page asks for it again, keeping the dialog.
  await action(page, 'staff.account-sync-role').click();
  const sync = page.getByRole('dialog', { name: '同步登录角色' });
  await expect(sync).toContainText('本人用手机号重新登录');
  await page.route('**/account/sync-role', (r) => r.fulfill({ status: 403, json: { code: 'STAFF_REAUTH_REQUIRED', message: '此操作需要重新验证企业账号' } }), { times: 1 });
  await sync.getByRole('button', { name: '同步登录角色' }).click();
  await expect(sync.getByRole('alert')).toContainText('重新验证');
  await expect(sync.getByRole('button', { name: '验证密码并继续' })).toBeVisible();
  // Reload restores the server's (still valid) password check.
  await page.reload();
  await action(page, 'staff.account-sync-role').click();
  // Timeout: the request never arrives; the retry reuses the same key (one execution).
  await page.route('**/account/sync-role', (r) => r.abort('timedout'), { times: 1 });
  await sync.getByRole('button', { name: '同步登录角色' }).click();
  await expect(sync.getByRole('alert')).toContainText('网络');
  await sync.getByRole('button', { name: '同步登录角色' }).dblclick();
  await expect(sync).toHaveCount(0);
  await expect(page.locator('.staff-title')).toContainText('待本人重新登录');
  await expect(page.getByTestId('role-progress')).toContainText('待本人重新登录');
  await expect(action(page, 'staff.account-sync-role')).toHaveCount(0);
  await shot(page, (mobile ? 'm-' : 'd-') + 'u1-02-synced');
  if (mobile) {
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(1);
  }

  // The member signs in again: manager, in effect.
  expect(await employeeSignIn(emp, phone)).toBe(200);
  const me = await (await emp.request.get('/api/v1/me')).json();
  expect(me.role).toBe('baobei_manager');
  await page.reload();
  await expect(page.locator('.staff-title')).toContainText('已启用');
  await expect(page.locator('.staff-facts')).toContainText('主管');
  await expect(page.getByTestId('role-progress')).toHaveCount(0);
  // Exactly one role sync was executed (double click and retry were not repeated).
  const audit = (await api(page, '/api/v1/staff/' + id + '/audit')).body.items as { action: string }[];
  expect(audit.filter((e) => e.action === 'staff.account.sync-role')).toHaveLength(1);
  await expect(page.locator('.staff-audit')).toContainText('同步登录角色');
  await shot(page, (mobile ? 'm-' : 'd-') + 'u1-03-active');
  await ctx.close();
});

test('同步登录角色仅管理员可见可用', async ({ page }, info) => {
  test.skip(info.project.name !== 'desktop', 'permission check is layout independent');
  await login(page, 'synthetic-manager');
  const r = await page.evaluate(async () => {
    const me = await (await fetch('/api/v1/me')).json();
    const res = await fetch('/api/v1/staff/00000000-0000-4000-8000-000000000001/account/sync-role', {
      method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': me.csrfToken, 'Idempotency-Key': 'acct-' + crypto.randomUUID().replace(/-/g, '') },
      body: '{}',
    });
    return res.status;
  });
  expect(r).toBe(403);
  await page.goto('/team/members');
  await expect(action(page, 'staff.account-sync-role')).toHaveCount(0);
});
