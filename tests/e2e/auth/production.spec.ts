import { test, expect, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { randomBytes } from 'node:crypto';
const origin = process.env.A1_WEB_ORIGIN!;
const password = () => readFileSync(process.env.A1_BROWSER_PASSWORD_FILE!, 'utf8').trim();
async function login(page: Page, user: string, updatePassword = false) {
  await page.goto('/');
  await page.getByRole('link', { name: '账号登录', exact: true }).click();
  expect(new URL(page.url()).origin).toBe(origin);
  await expect(page.locator('link[href*="udao.css"]')).toHaveCount(1);
  await expect(page.locator('#kc-header-wrapper')).toContainText('U·DAO');
  await expect(page.locator('#username')).toHaveCSS('background-color', 'rgb(6, 21, 45)');
  await expect(page.locator('#kc-login')).toHaveCSS('background-image', /linear-gradient/);
  await page.locator('#username').fill(user);
  await page.locator('#password').fill(password());
  await page.locator('#kc-login').click();
  if (updatePassword) {
    await expect(page.locator('#password-new')).toBeVisible();
    expect((await page.request.get('/auth/session')).status()).toBe(401);
    const replacement = randomBytes(32).toString('hex');
    await page.locator('#password-new').fill(replacement);
    await page.locator('#password-confirm').fill(replacement);
    await page.locator('#kc-passwd-update-form input[type="submit"]').click();
  }
  await expect(page.getByTestId('session-user')).toContainText(user);
}

test('真实 Keycloak Code + PKCE 建立 HttpOnly Cookie，viewer 项目隔离、CSRF 与退出失效', async ({ page, context }) => {
  await login(page, 'a1-viewer');
  await expect(page.getByTestId('session-projects')).not.toContainText('P002');
  const cookie = (await context.cookies(origin)).find(item => item.name === '__Host-crane_session');
  expect(Boolean(cookie)).toBe(true); expect(cookie?.secure).toBe(true); expect(cookie?.httpOnly).toBe(true); expect(cookie?.sameSite).toBe('Lax'); expect(cookie?.path).toBe('/');
  expect(await page.evaluate(() => document.cookie.includes('__Host-crane_session'))).toBe(false);
  expect(await page.evaluate(() => [localStorage.length, sessionStorage.length])).toEqual([0, 0]);
  expect(await page.getByTestId('production-session').textContent()).not.toContain('开发凭据');
  const results = await page.evaluate(async () => {
    const current = await (await fetch('/auth/session')).json();
    const projects = await (await fetch('/api/v1/projects')).json();
    const missing = await fetch('/api/v1/devices/SIM-P001-01/live-sessions', { method: 'POST' });
    const viewer = await fetch('/api/v1/devices/SIM-P001-01/live-sessions', { method: 'POST', headers: { 'X-CSRF-Token': current.csrfToken } });
    const cross = await fetch('/api/v1/devices/SIM-P002-01/shadow');
    const dev = await fetch('/auth/dev-session', { method: 'POST' });
    const logout = await fetch('/auth/logout', { method: 'POST', headers: { 'X-CSRF-Token': current.csrfToken } });
    const after = await fetch('/auth/session');
    return { projects: projects.map((item: { id: string }) => item.id), missing: missing.status, viewer: viewer.status, cross: cross.status, dev: dev.status, logout: logout.status, after: after.status };
  });
  expect(results).toEqual({ projects: ['P001'], missing: 403, viewer: 403, cross: 404, dev: 404, logout: 204, after: 401 });
  await page.reload(); await expect(page.getByRole('link', { name: '账号登录', exact: true })).toBeVisible();
});

test('operator 仅操作 P002 LIVE，管理员也不能跨项目读取', async ({ page }) => {
  await login(page, 'a1-operator');
  const results = await page.evaluate(async () => {
    const current = await (await fetch('/auth/session')).json();
    const headers = { 'X-CSRF-Token': current.csrfToken };
    const open = await fetch('/api/v1/devices/SIM-P002-01/live-sessions', { method: 'POST', headers });
    const session = await open.json();
    const renew = await fetch(`/api/v1/live-sessions/${session.sessionId}/renew`, { method: 'POST', headers });
    const close = await fetch(`/api/v1/live-sessions/${session.sessionId}`, { method: 'DELETE', headers });
    const cross = await fetch('/api/v1/devices/SIM-P001-01');
    return { open: open.status, renew: renew.status, close: close.status, cross: cross.status };
  });
  expect(results).toEqual({ open: 201, renew: 200, close: 204, cross: 404 });
  await page.getByRole('button', { name: '退出', exact: true }).click();
  // Keycloak may require explicit logout confirmation because no id_token is sent to the browser.
  // The inherited Keycloak template has a stable id across supported locales.
  const confirm = page.locator('#kc-logout');
  await expect(confirm).toBeVisible();
  await confirm.click();
  await page.waitForURL(url => url.origin === origin && url.pathname === '/');
  await expect(page.getByRole('link', { name: '账号登录', exact: true })).toBeVisible();
});

test('scoped platform_admin 没有全局项目读取权', async ({ page }) => {
  await login(page, 'a1-admin', true);
  const values = await page.evaluate(async () => ({ projects: await (await fetch('/api/v1/projects')).json(), cross: (await fetch('/api/v1/devices/SIM-P002-01')).status }));
  expect(values.projects.map((item: { id: string }) => item.id)).toEqual(['P001']); expect(values.cross).toBe(404);
});
