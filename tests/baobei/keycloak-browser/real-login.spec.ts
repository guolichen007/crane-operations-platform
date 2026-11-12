// G4-S5-R1-E real-browser check against a real Keycloak 26.8.1 (loopback fixture).
// Started by tests/baobei/keycloak-browser.sh; never runs in CI without a fixture.
import { test, expect } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';

const KC = process.env.BAOBEI_KEYCLOAK_URL!;
const phone = process.env.BAOBEI_KC_PHONE!;
const temp = process.env.BAOBEI_KC_TEMP!;
const newPw = 'Real-' + Math.random().toString(36).slice(2, 10) + 'A1';
const newerPw = 'Real-' + Math.random().toString(36).slice(2, 10) + 'B2';

test('真实 Keycloak：11 位手机号 + 初始密码登录，强制改密，个人修改密码，退出后必须重新输入密码', async ({ page }) => {
  test.skip(!KC || !phone || !temp, 'NOT_RUN: real Keycloak fixture required');
  await page.goto('/login');
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(new RegExp('^' + KC.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '/realms/baobei/'));
  await expect(page.getByText('手机号', { exact: true })).toBeVisible();
  await expect(page.getByText('友道智造 · 项目报备中心')).toBeVisible();
  await page.screenshot({ path: process.env.SHOTS + '/kc-01-login.png' });
  await page.locator('#username').fill(phone);
  await page.locator('#password').fill(temp);
  await page.locator('#kc-login').click();
  // First login: Keycloak forces a new password; no email or profile prompt.
  await expect(page.locator('#password-new')).toBeVisible();
  await expect(page.locator('#email')).toHaveCount(0);
  await page.screenshot({ path: process.env.SHOTS + '/kc-02-update-password.png' });
  await page.locator('#password-new').fill(newPw);
  await page.locator('#password-confirm').fill(newPw);
  await page.locator('[type=submit]').first().click();
  await expect(page.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
  await expect(page.locator('.workspace-label')).toHaveText('销售工作空间');
  await page.screenshot({ path: process.env.SHOTS + '/kc-03-workspace.png' });
  // Self-service password change: Keycloak asks for the current password first.
  await page.locator('[data-action-id="auth.account"]:visible').click();
  await page.locator('[data-action-id="auth.change-password"]').click();
  // Keycloak first verifies the current password (sign-in form), then asks for the new one.
  await expect(page.locator('#password')).toBeVisible();
  await page.screenshot({ path: process.env.SHOTS + '/kc-04-change-password-current.png' });
  // The phone is shown read-only on this page (cannot be changed by the employee).
  const fixed = page.getByRole('textbox').filter({ hasNot: page.locator('#password') }).first();
  await expect(fixed).toHaveValue(phone);
  await expect(fixed).not.toBeEditable();
  await page.locator('#password').fill(newPw);
  await page.locator('#kc-login').click();
  await expect(page.locator('#password-new')).toBeVisible();
  await page.locator('#password-new').fill(newerPw);
  await page.locator('#password-confirm').fill(newerPw);
  await page.locator('[type=submit]').first().click();
  await expect(page.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
  // Sign out; the next sign-in must ask for the password again (no silent SSO).
  await page.locator('[data-action-id="auth.account"]:visible').click();
  await page.locator('[data-action-id="auth.logout"]').click();
  await expect(page).toHaveURL(/\/login$/);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page.locator('#password')).toBeVisible();
  // Empty phone field: another person can sign in on this browser.
  await expect(page.locator('#username')).toBeEditable();
  await expect(page.locator('#username')).toHaveValue('');
  await page.screenshot({ path: process.env.SHOTS + '/kc-05-after-logout.png' });
  // Old password rejected, newest accepted.
  await page.locator('#username').fill(phone);
  await page.locator('#password').fill(newPw);
  await page.locator('#kc-login').click();
  await expect(page.locator('#password')).toBeVisible();
  await page.locator('#username').fill(phone);
  await page.locator('#password').fill(newerPw);
  await page.locator('#kc-login').click();
  await expect(page.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
});
