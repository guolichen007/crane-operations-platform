import { type Page, expect } from '@playwright/test';
import { readFileSync } from 'node:fs';

const origin = process.env.A1_WEB_ORIGIN!;
const password = () => readFileSync(process.env.A1_BROWSER_PASSWORD_FILE!, 'utf8').trim();

// Real Keycloak Code + PKCE login against the isolated anonymous fixture realm.
// B4 identities use a fixed (non-temporary) password, so this never triggers the
// A1 first-login password-change flow.
export async function login(page: Page, user: string): Promise<void> {
  await page.goto('/');
  await page.getByRole('link', { name: '账号登录', exact: true }).click();
  await expect(page.locator('#username')).toBeVisible();
  await page.locator('#username').fill(user);
  await page.locator('#password').fill(password());
  await page.locator('#kc-login').click();
  await expect(page.getByTestId('session-user')).toContainText(user);
}

// Fetch the current session (including CSRF token) from inside the browser
// context, mirroring the pattern used by tests/e2e/auth/production.spec.ts.
export async function sessionCsrf(page: Page): Promise<string> {
  return page.evaluate(async () => {
    const session = await (await fetch('/auth/session')).json();
    return session.csrfToken as string;
  });
}
