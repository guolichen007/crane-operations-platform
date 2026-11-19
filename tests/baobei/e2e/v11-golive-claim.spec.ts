import { type Page } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
import { test, expect, action, uniq, api } from './support/isolation';

// V1.1 go-live: first-time account claim. The administrator adds a member with a
// temporary login name and opens the login from the page; the member signs in
// with that name (development account service = same contract as Keycloak),
// sees only the claim page, enters their own name and phone, and signs in again
// with the phone. Nothing else is reachable before the claim.
async function login(page: Page, subject: string) {
  await page.goto('/login');
  await page.locator('select').selectOption(subject);
  await action(page, 'auth.dev-login').click();
  await expect(page.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
}
async function signIn(page: Page, username: string) {
  return page.evaluate(async (phone) => {
    const r = await fetch('/auth/dev-phone', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ phone }) });
    return r.status;
  }, username);
}
const phoneFor = (seed: string) => {
  let h = 7;
  for (const ch of seed) h = (h * 41 + ch.charCodeAt(0)) % 100_000_000;
  return '135' + String(h).padStart(8, '0');
};

test('首次认领：临时登录名 → 本人姓名与手机号 → 手机号重新登录', async ({ page, browser, baseURL }, info) => {
  const mobile = info.project.name !== 'desktop';
  const unique = uniq('CL');
  const temp = ('cl' + unique.replace(/[^a-z0-9]/gi, '').toLowerCase()).slice(0, 24);
  const phone = phoneFor(unique + (mobile ? 'm' : 'd'));
  await login(page, 'synthetic-admin');
  await page.goto('/settings/users');
  await action(page, 'staff.invite').click();
  const add = page.getByRole('dialog', { name: '添加成员' });
  await add.getByLabel(/^姓名/).fill('待认领' + unique.slice(-4));
  await add.getByLabel(/发放临时登录名/).check();
  await expect(add.getByLabel(/^工作手机/)).toHaveCount(0);
  await add.getByLabel(/^临时登录名/).fill(temp);
  await add.getByLabel(/^团队/).selectOption({ index: 1 });
  await add.getByRole('button', { name: '添加成员' }).click();
  await expect(page.locator('.staff-title')).toContainText('待开通');
  await expect(page.locator('.staff-facts')).toContainText('待本人首次登录时填写');
  await action(page, 'staff.account-create').click();
  const open = page.getByRole('dialog', { name: '开通登录账号' });
  await expect(open).toContainText(temp);
  await open.getByRole('button', { name: '开通账号' }).click();
  await expect(page.getByText(temp).first()).toBeVisible();
  await action(page, 'staff.credential-done').click();
  await expect(page.getByTestId('account-state')).toContainText('待本人首次认领');

  // The member signs in with the temporary name: only the claim page.
  const ctx = await browser.newContext({ baseURL, ...(mobile ? { viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } : {}) });
  const emp = await ctx.newPage();
  await emp.goto('/login');
  expect(await signIn(emp, temp)).toBe(200);
  await emp.goto('/projects');
  await expect(emp).toHaveURL(/\/claim$/);
  await expect(emp.getByRole('heading', { name: '完成首次登录' })).toBeVisible();
  await expect(emp.locator('nav a')).toHaveCount(0);
  expect((await emp.request.get('/api/v1/projects')).status()).toBe(403);
  // Validation stays on the page.
  await emp.getByLabel(/^真实姓名/).fill('12345');
  await emp.getByLabel(/^本人手机号/).fill('123');
  await action(emp, 'claim.submit').click();
  await expect(emp.getByText('姓名不能只包含数字')).toBeVisible();
  await expect(emp.getByText('请填写本人的 11 位中国大陆手机号')).toBeVisible();
  await emp.getByLabel(/^真实姓名/).fill('认领' + unique.slice(-3));
  await emp.getByLabel(/^本人手机号/).fill(phone);
  const claimed = emp.waitForResponse((r) => r.url().endsWith('/api/v1/me/claim'));
  await action(emp, 'claim.submit').click();
  const res = await claimed;
  expect(res.status(), await res.text()).toBe(200);
  await expect(emp.getByRole('heading', { name: '认领完成' })).toBeVisible();
  await expect(emp.getByText(phone)).toBeVisible();
  // The temporary name stops working; the phone signs in and reaches the workspace.
  expect(await signIn(emp, temp)).toBe(403);
  await action(emp, 'claim.sign-in-again').click();
  await expect(emp).toHaveURL(/\/login/);
  expect(await signIn(emp, phone)).toBe(200);
  await emp.goto('/');
  await expect(emp.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
  const me = await api(emp, '/api/v1/me');
  expect(me.body.claimRequired).toBe(false);
  expect(me.body.name).toBe('认领' + unique.slice(-3));
  await ctx.close();
  // The administrator sees the phone login now.
  await page.reload();
  await expect(page.getByTestId('account-state')).toContainText(phone);
  await expect(page.getByTestId('account-state')).not.toContainText('待本人首次认领');
});
