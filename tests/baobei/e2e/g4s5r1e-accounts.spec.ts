import { type Page } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
import { test, expect, action, uniq } from './support/isolation';

// G4-S5-R1-E: administrator creates a real login account in the page, sees the
// one-time password once, resets it, changes the login phone and disables /
// restores the account. Development runs the in-memory account service with the
// same contract as the Keycloak adapter (real-Keycloak runs: tests/baobei/keycloak-*.py).
async function login(page: Page, subject: string) {
  await page.goto('/login');
  await page.locator('select').selectOption(subject);
  await action(page, 'auth.dev-login').click();
  await expect(page.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
}
const phoneFor = (seed: string, slot: number) => {
  let h = slot;
  for (const ch of seed) h = (h * 33 + ch.charCodeAt(0)) % 100_000_000;
  return '136' + String(h).padStart(8, '0');
};
async function employeeSignIn(page: Page, phone: string) {
  return page.evaluate(async (phone) => {
    const r = await fetch('/auth/dev-phone', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ phone }) });
    return r.status;
  }, phone);
}

test('管理员网页开户：一次性初始密码、重置、换手机号、停用恢复，员工手机号登录', async ({ page, browser, baseURL }, info) => {
  test.skip(info.project.name !== 'desktop', 'one full account loop per run; mobile layout covered by the staff spec');
  const unique = uniq('R1E');
  const phone = phoneFor(unique, 1), phone2 = phoneFor(unique, 2);
  await login(page, 'synthetic-admin');
  await page.goto('/settings/users');
  await action(page, 'staff.invite').click();
  const dialog = page.getByRole('dialog', { name: '添加成员' });
  await dialog.getByLabel(/^姓名/).fill('合成开户' + unique.slice(-4));
  await dialog.getByLabel(/^工作手机/).fill(phone);
  await dialog.getByLabel(/^团队/).selectOption({ index: 1 });
  await dialog.getByRole('button', { name: '添加成员' }).click();
  await expect(page.locator('.staff-title')).toContainText('待开通');
  await expect(page.getByText('请开通登录账号', { exact: true })).toBeVisible();
  // No JSON receipts or Keycloak id inputs anywhere.
  await expect(page.locator('pre')).toHaveCount(0);
  await expect(page.getByText('Keycloak', { exact: false })).toHaveCount(0);

  // Create: network failure keeps the dialog; retry uses the same request (no duplicate).
  await action(page, 'staff.account-create').click();
  const create = page.getByRole('dialog', { name: '开通登录账号' });
  await page.route('**/account/create', (r) => r.abort('internetdisconnected'), { times: 1 });
  await create.getByRole('button', { name: '开通账号' }).click();
  await expect(create.getByRole('alert')).toContainText('网络');
  await create.getByRole('button', { name: '开通账号' }).click();
  const cred = page.getByRole('dialog', { name: '登录账号已开通' });
  await expect(cred.getByTestId('credential-login')).toHaveText(phone);
  const temp = (await cred.getByTestId('credential-password').innerText()).trim();
  expect(temp).toMatch(/^[A-Za-z0-9]{12}$/);
  await action(page, 'staff.credential-done').click();
  await expect(cred).toHaveCount(0);
  // After closing, the password is nowhere on the page, and a reload does not bring it back.
  await expect(page.getByText(temp)).toHaveCount(0);
  await page.reload();
  await expect(page.getByText(temp)).toHaveCount(0);
  await expect(page.locator('.staff-facts')).toContainText(phone + ' · 可登录（待首次修改密码）');
  await expect(page.locator('.staff-title')).toContainText('待首次登录');

  // The employee signs in with the 11-digit phone and lands in the sales workspace.
  const ctx = await browser.newContext({ baseURL });
  const emp = await ctx.newPage();
  await emp.goto('/login');
  expect(await employeeSignIn(emp, phone)).toBe(200);
  await emp.goto('/');
  await expect(emp.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
  await expect(emp.locator('.workspace-label')).toHaveText('销售工作空间');
  await page.reload();
  await expect(page.locator('.staff-title')).toContainText('已启用');

  // Reset password: new one-time password, the employee's session ends.
  await action(page, 'staff.account-reset').click();
  const reset = page.getByRole('dialog', { name: '重置登录密码' });
  await reset.getByLabel(/^操作原因/).fill('员工忘记密码');
  await reset.getByRole('button', { name: '重置密码' }).click();
  const cred2 = page.getByRole('dialog', { name: '密码已重置' });
  const temp2 = (await cred2.getByTestId('credential-password').innerText()).trim();
  expect(temp2).not.toBe(temp);
  await action(page, 'staff.credential-done').click();
  expect((await emp.request.get('/api/v1/me')).status()).toBe(401);

  // Change the login phone: duplicate refused in the dialog; then success; old phone dead.
  await action(page, 'staff.account-phone').click();
  const change = page.getByRole('dialog', { name: '更换登录手机号' });
  await change.getByLabel(/^新登录手机号/).fill('12345');
  await change.getByLabel(/^操作原因/).fill('员工换号');
  await change.getByRole('button', { name: '更换手机号' }).click();
  await expect(change.getByText('手机格式不正确', { exact: false }).first()).toBeVisible();
  await change.getByLabel(/^新登录手机号/).fill(phone2);
  await change.getByRole('button', { name: '更换手机号' }).click();
  await expect(change).toHaveCount(0);
  await expect(page.locator('.staff-facts')).toContainText(phone2);
  await emp.goto('/login');
  expect(await employeeSignIn(emp, phone)).toBe(403);
  expect(await employeeSignIn(emp, phone2)).toBe(200);

  // Disable: immediate; restore: account usable again.
  await action(page, 'staff.account-disable').click();
  const disable = page.getByRole('dialog', { name: '停用登录' });
  await disable.getByLabel(/^操作原因/).fill('离职');
  await disable.getByRole('button', { name: '停用登录' }).click();
  await expect(page.locator('.staff-title')).toContainText('已停用');
  expect((await emp.request.get('/api/v1/me')).status()).toBe(401);
  await emp.goto('/login');
  expect(await employeeSignIn(emp, phone2)).toBe(403);
  await action(page, 'staff.account-enable').click();
  await page.getByRole('dialog', { name: '恢复登录' }).getByRole('button', { name: '恢复登录' }).click();
  await expect(page.locator('.staff-title')).toContainText('待首次登录');
  expect(await employeeSignIn(emp, phone2)).toBe(200);
  // Audit trail shows every account action, without passwords.
  for (const label of ['开通登录账号', '重置登录密码', '更换登录手机号', '停用登录', '恢复登录'])
    await expect(page.locator('.staff-audit')).toContainText(label);
  await expect(page.locator('.staff-audit')).not.toContainText(temp);
  await ctx.close();
});

test('员工不能使用账号管理入口；个人菜单提供修改密码', async ({ page }) => {
  await login(page, 'synthetic-a');
  const r = await page.evaluate(async () => {
    const me = await (await fetch('/api/v1/me')).json();
    const res = await fetch('/api/v1/staff/00000000-0000-4000-8000-000000000001/account/reset-password', {
      method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': me.csrfToken, 'Idempotency-Key': 'acct-' + crypto.randomUUID().replace(/-/g, '') },
      body: JSON.stringify({ reason: 'x' }),
    });
    return res.status;
  });
  expect(r).toBe(403);
  await action(page, 'auth.account').click();
  await expect(page.getByRole('dialog', { name: '个人账号' }).locator('[data-action-id="auth.change-password"]')).toBeVisible();
});
