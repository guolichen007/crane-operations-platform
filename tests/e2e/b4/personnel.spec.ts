import { test, expect, type Page, type BrowserContext } from '@playwright/test';
import { login } from './login';
import { RUN_ID, assertWriteTarget } from './run';

// P1 personnel / account management on the REAL stack: Keycloak 26.8.1 (crane realm, restricted
// crane-account-service), production BFF session + CSRF, PostgreSQL, Chromium. b4-admin was made a
// managed system_admin by the fixture through the production host recovery path (seed-personnel).
//
// One continuous flow with one run-unique person p1-<RUN_ID>:
//   web account opening (3 steps) → one-time password → FIRST LOGIN forces a password change →
//   portfolio role grant takes effect in page + API → cross-portfolio refusal → disable while the
//   person is logged in (cookie + WebSocket stop) → enable → password reset (old password and old
//   session dead) → duplicate opening refused → last administrator protected → anonymous / non-admin
//   refused → mobile layout. verify-personnel-state then checks PostgreSQL.

const username = `p1-${RUN_ID.toLowerCase()}`;
const origin = process.env.A1_WEB_ORIGIN!;

async function keycloakLogin(page: Page, user: string, password: string): Promise<'ok' | 'update' | 'rejected'> {
  await page.goto('/');
  await page.getByRole('link', { name: '账号登录', exact: true }).click();
  await expect(page.locator('#username')).toBeVisible();
  await page.locator('#username').fill(user);
  await page.locator('#password').fill(password);
  // Submitting always navigates (POST to Keycloak's login-actions); wait for that response so the
  // still-visible old form is never mistaken for a refusal.
  await Promise.all([page.waitForNavigation({ waitUntil: 'domcontentloaded' }), page.locator('#kc-login').click()]);
  // Exactly one of three end states: Keycloak asks for a new password, the app shows the session,
  // or the Keycloak login form is shown again (wrong / disabled credentials).
  const update = page.locator('#password-new');
  const session = page.getByTestId('session-user');
  const again = page.locator('#kc-login').or(page.locator('#kc-error-message'));
  await expect(update.or(session).or(again)).toBeVisible();
  if (await update.isVisible()) return 'update';
  if (await session.isVisible()) return 'ok';
  return 'rejected';
}

async function readOneTimePassword(page: Page): Promise<string> {
  const otp = page.getByTestId('one-time-password');
  await expect(otp).toBeVisible();
  await otp.getByTestId('otp-reveal').click();
  const value = (await otp.getByTestId('otp-password').textContent())!.trim();
  expect(value).toMatch(/^[A-Za-z0-9]{16}$/);
  await otp.getByTestId('otp-confirm').check();
  await otp.getByTestId('otp-close').click();
  await expect(otp).toBeHidden();
  return value;
}

async function api(page: Page, method: string, path: string, body?: unknown, headers: Record<string, string> = {}) {
  return page.evaluate(async ({ method, path, body, headers }) => {
    const session = await (await fetch('/auth/session')).json().catch(() => ({}));
    const res = await fetch(path, {
      method,
      headers: { 'Content-Type': 'application/json', ...(method === 'GET' ? {} : { 'X-CSRF-Token': session.csrfToken ?? '' }), ...headers },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const text = await res.text();
    return { status: res.status, text: text.slice(0, 400) };
  }, { method, path, body, headers });
}

test('P1 人员与账号：网页开户 → 首次登录改密 → 授权生效 → 停用即断 → 重置密码 → 防重/最后管理员/越权', async ({ browser }) => {
  test.setTimeout(240_000);
  const adminCtx = await browser.newContext();
  const admin = await adminCtx.newPage();
  await login(admin, 'b4-admin');
  await assertWriteTarget(admin);

  // ---- 1. web account opening (3 steps), with a portfolio role -------------------------------
  await admin.goto('/#/overview');
  await admin.getByTestId('rail-system').click();
  await expect(admin.getByTestId('people-table')).toBeVisible();
  await admin.getByTestId('person-create').click();
  const wiz = admin.getByTestId('person-wizard');
  await wiz.getByTestId('wiz-name').fill(`P1 验收员 ${RUN_ID}`);
  await wiz.getByTestId('wiz-username').fill(username);
  await wiz.getByTestId('wiz-department').selectOption('b4-ops');
  await wiz.getByTestId('wiz-job').fill('调度员');
  await wiz.getByTestId('wiz-next').click();
  await wiz.getByTestId('wiz-add-scope').click();
  await wiz.getByTestId('wiz-scope-0').selectOption('PS-B4');
  await wiz.getByTestId('wiz-scope-role-0').selectOption('portfolio_planner');
  await wiz.getByTestId('wiz-next').click();
  await wiz.getByTestId('wiz-next').click();
  const temporary = await readOneTimePassword(admin);
  await expect(admin.getByTestId('person-username')).toHaveText(username);
  await expect(admin.getByTestId('person-identity')).toContainText('待首次改密');
  await admin.screenshot({ path: 'test-results/b4-visual/p1-person-created.png' });

  // ---- 2. duplicate opening refused (409) ------------------------------------------------------
  const dup = await api(admin, 'POST', '/api/v1/admin/people', { username, profile: { displayName: 'dup', departmentId: '', jobTitle: '', phone: '', email: '' }, systemRoles: [], portfolioGrants: [] }, { 'Idempotency-Key': 'dup-' + RUN_ID + '-0001' });
  expect(dup.status).toBe(409);

  // ---- 3. first login forces a password change -------------------------------------------------
  const userCtx = await browser.newContext();
  const user = await userCtx.newPage();
  expect(await keycloakLogin(user, username, temporary)).toBe('update');
  const chosen = `P1-${RUN_ID}-Chg9x`;
  await user.locator('#password-new').fill(chosen);
  await user.locator('#password-confirm').fill(chosen);
  await user.locator('#kc-passwd-update-form input[type="submit"], #kc-passwd-update-form button[type="submit"]').first().click();
  await expect(user.getByTestId('session-user')).toContainText(`P1 验收员`);
  await user.screenshot({ path: 'test-results/b4-visual/p1-first-login.png' });

  // ---- 4. granted portfolio role works in page and API; no system/admin access; cross scope refused
  const scopes = await api(user, 'GET', '/api/v1/portfolio/scopes');
  expect(scopes.status).toBe(200);
  expect(scopes.text).toContain('PS-B4');
  await user.goto('/#/portfolio');
  await expect(user.getByTestId('intake-create')).toBeVisible();
  expect((await api(user, 'GET', '/api/v1/admin/people')).status).toBe(403);
  await expect(user.getByTestId('rail-system')).toHaveCount(0);
  // Planner may not approve and may not search people for project grants (manager-only).
  expect((await api(user, 'POST', '/api/v1/portfolio/intakes/none/approve', {})).status).toBeGreaterThanOrEqual(403);
  // Project data of a project never granted: 404/empty, never leaked.
  const assets = await api(user, 'GET', '/api/v1/assets?project=P001');
  expect(assets.text).not.toContain('"projectId":"P001"');

  // Role change takes effect immediately for the same session (planner → removed).
  admin.once('dialog', (d) => void d.accept());
  await admin.getByTestId('person-detail').locator('[data-scope="PS-B4"]').getByRole('button', { name: '撤销' }).click();
  await expect(admin.getByTestId('person-notice')).toContainText('已撤销');
  const after = await api(user, 'GET', '/api/v1/portfolio/scopes');
  expect(after.text).not.toContain('PS-B4');
  // …and re-granted for the remaining flow.
  await admin.getByTestId('detail-scope').selectOption('PS-B4');
  await admin.getByTestId('detail-scope-role').selectOption('portfolio_planner');
  await admin.getByTestId('detail-scope-add').click();
  await expect(admin.getByTestId('detail-portfolio')).toContainText('B4');

  // ---- 5. disable while logged in: cookie and WebSocket stop -----------------------------------
  const ws = await user.evaluate(() => new Promise<string>((resolve) => {
    const s = new WebSocket(location.origin.replace('https', 'wss') + '/api/v1/devices/SIM-P001-01/stream');
    s.onclose = (e) => resolve('closed:' + e.code);
    s.onerror = () => resolve('error');
    setTimeout(() => resolve('timeout'), 4000);
  }));
  // The person holds no project grant, so the stream is refused at once (project scope first). The
  // "disable cuts an OPEN stream" case is proven with a granted operator in
  // personnel_http_test.go (TestPersonnelAPIProvisionDisableResetAndLiveRevocation).
  expect(ws).not.toBe('timeout');
  admin.once('dialog', (d) => void d.accept());
  await admin.getByTestId('person-disable').click();
  await expect(admin.getByTestId('person-notice')).toContainText('已停用');
  await expect(admin.getByTestId('person-status')).toHaveText('已停用');
  expect((await api(user, 'GET', '/api/v1/portfolio/scopes')).status).toBe(401);
  // Identity-provider login is refused while disabled.
  const blocked = await (await browser.newContext()).newPage();
  expect(await keycloakLogin(blocked, username, chosen)).toBe('rejected');
  admin.once('dialog', (d) => void d.accept());
  await admin.getByTestId('person-enable').click();
  await expect(admin.getByTestId('person-status')).toContainText('启用');

  // ---- 6. password reset: old password and old sessions dead, new temporary forces change -----
  const second = await (await browser.newContext()).newPage();
  expect(await keycloakLogin(second, username, chosen)).toBe('ok');
  admin.once('dialog', (d) => void d.accept());
  await admin.getByTestId('person-reset').click();
  const reset = await readOneTimePassword(admin);
  expect(reset).not.toBe(temporary);
  expect((await api(second, 'GET', '/api/v1/portfolio/scopes')).status).toBe(401);
  const third = await (await browser.newContext()).newPage();
  expect(await keycloakLogin(third, username, chosen)).toBe('rejected');
  const fourth = await (await browser.newContext()).newPage();
  expect(await keycloakLogin(fourth, username, reset)).toBe('update');

  // ---- 7. last administrator & self protection; history -----------------------------------------
  const me = await api(admin, 'PUT', '/api/v1/admin/people/b4-admin/system-roles', { roles: [], expectedRoles: ['system_admin'] });
  expect(me.status).toBe(422);
  await expect(admin.getByTestId('person-history')).toContainText('重置密码');
  await admin.screenshot({ path: 'test-results/b4-visual/p1-person-detail.png', fullPage: true });

  // ---- 8. anonymous and non-admin refused ---------------------------------------------------------
  const anon = await (await browser.newContext()).newPage();
  await anon.goto('/');
  expect((await api(anon, 'GET', '/api/v1/admin/people')).status).toBe(401);
  const viewer = await (await browser.newContext()).newPage();
  await login(viewer, 'b4-viewer');
  expect((await api(viewer, 'GET', '/api/v1/admin/people')).status).toBe(403);
  expect((await api(viewer, 'POST', '/api/v1/admin/people/b4-admin/disable', {}, { 'If-Match': '"1"' })).status).toBe(403);

  // ---- 9. mobile layout of the real page -------------------------------------------------------
  const mobileCtx: BrowserContext = await browser.newContext({ viewport: { width: 375, height: 812 }, storageState: await adminCtx.storageState() });
  const mobile = await mobileCtx.newPage();
  await mobile.goto(origin + '/#/system/people');
  await expect(mobile.getByTestId('people-table')).toBeVisible();
  const overflow = await mobile.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
  await mobile.screenshot({ path: 'test-results/b4-visual/p1-people-375.png' });
});
