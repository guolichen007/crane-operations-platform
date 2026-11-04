import { test, expect, type Page } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';

async function control(page: Page, path: string) {
  expect((await page.request.post('/fixture/' + path)).status()).toBe(204);
}

async function metrics(page: Page) {
  return (await page.request.get('/fixture/metrics')).json() as Promise<{ sessions: number; exchanges: number }>;
}

async function begin(page: Page, mode = 'success') {
  await control(page, 'mode?value=' + mode);
  await page.goto('/login');
  await expect(page.getByText('仅限本地开发', { exact: false })).toHaveCount(0);
  await page.getByRole('button', { name: /企业账号登录|重新登录/ }).click();
  await expect(page.getByRole('heading', { name: '隔离企业认证' })).toBeVisible();
  const url = new URL(page.url());
  expect(url.hostname).toBe('localhost');
  expect(url.searchParams.get('code_challenge_method')).toBe('S256');
  expect(url.searchParams.get('state')).toBeTruthy();
  const binding = (await page.context().cookies()).find(c => c.name === '__Host-baobei_login');
  expect(binding).toMatchObject({ httpOnly: true, secure: true, sameSite: 'Lax', path: '/' });
}

async function authorize(page: Page) {
  await page.getByRole('link', { name: '继续企业登录' }).click();
}

async function workspace(page: Page) {
  await expect(page.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
  const session = (await page.context().cookies()).find(c => c.name === '__Host-baobei_session');
  expect(session).toMatchObject({ httpOnly: true, secure: true, sameSite: 'Lax', path: '/' });
}

async function failure(page: Page, code: string, message: string, sessions: number) {
  await expect(page).toHaveURL('/login?error=' + code);
  await expect(page.getByRole('alert')).toContainText(message);
  await expect(page.getByRole('button', { name: /重新登录/ })).toBeEnabled();
  await expect(page.getByText('如仍无法登录，请联系系统管理员。', { exact: true })).toBeVisible();
  expect((await page.request.get('/api/v1/me')).status()).toBe(401);
  expect((await page.context().cookies()).some(c => c.name === '__Host-baobei_session')).toBe(false);
  expect((await metrics(page)).sessions).toBe(sessions);
}

test('生产模式授权登录、刷新、后退及回调重放', async ({ page }) => {
  await begin(page);
  const callback = await page.getByRole('link', { name: '继续企业登录' }).getAttribute('href');
  expect(callback).toBeTruthy();
  await authorize(page);
  await workspace(page);
  const previous = await metrics(page);
  await page.reload();
  await workspace(page);
  await page.goBack();
  expect(await metrics(page)).toEqual(previous);
  await page.goto(callback!);
  await expect(page).toHaveURL('/login?error=LOGIN_STATE_INVALID');
  await expect(page.getByRole('alert')).toContainText('已使用');
  expect(await metrics(page)).toEqual(previous);
});

test('取消登录消耗状态并允许重新登录', async ({ page }) => {
  const previous = await metrics(page);
  await begin(page);
  const callback = await page.getByRole('link', { name: '继续企业登录' }).getAttribute('href');
  await page.getByRole('link', { name: '取消企业登录' }).click();
  await failure(page, 'LOGIN_CANCELLED', '您已取消企业登录', previous.sessions);
  await page.goto(callback!);
  await failure(page, 'LOGIN_STATE_INVALID', '已使用', previous.sessions);
  expect(await metrics(page)).toEqual(previous);
  await begin(page);
  await authorize(page);
  await workspace(page);
});

test('过期 state 拒绝后重新登录', async ({ page }) => {
  const previous = await metrics(page);
  await begin(page);
  await control(page, 'expire-state');
  await authorize(page);
  await failure(page, 'LOGIN_EXPIRED', '登录请求已过期', previous.sessions);
  await begin(page);
  await authorize(page);
  await workspace(page);
});

test('缺少绑定 Cookie 不兑换授权码', async ({ page }) => {
  const previous = await metrics(page);
  await begin(page);
  await page.context().clearCookies({ name: '__Host-baobei_login' });
  await authorize(page);
  await failure(page, 'LOGIN_STATE_INVALID', '请允许本站 Cookie', previous.sessions);
  expect(await metrics(page)).toEqual(previous);
});

for (const [mode, code, message] of [
  ['oauth-error', 'LOGIN_PROVIDER_REJECTED', '企业认证未完成'],
  ['exchange', 'LOGIN_EXCHANGE_FAILED', '企业登录未能完成'],
  ['token', 'LOGIN_TOKEN_INVALID', '无法验证企业身份'],
  ['nonce', 'LOGIN_NONCE_INVALID', '无法确认本次企业登录'],
  ['unopened', 'LOGIN_NOT_PROVISIONED', '尚未开通'],
  ['role', 'LOGIN_ROLE_MISSING', '缺少报备中心访问权限'],
  ['disabled', 'LOGIN_DISABLED', '已停用'],
  ['email', 'LOGIN_EMAIL_MISMATCH', '与开通信息不一致'],
  ['unverified', 'LOGIN_EMAIL_MISMATCH', '尚未验证'],
  ['unavailable', 'LOGIN_PROVIDER_UNAVAILABLE', '认证服务暂时不可用'],
  ['network', 'LOGIN_PROVIDER_UNAVAILABLE', '认证服务暂时不可用'],
] as const) {
  test('生产回调分类 ' + mode, async ({ page }) => {
    const previous = await metrics(page);
    await begin(page, mode);
    await authorize(page);
    await failure(page, code, message, previous.sessions);
    await expect(page.locator('body')).not.toContainText('sensitive-fixture-description');
    if (mode === 'network' || mode === 'unavailable') {
      await control(page, 'mode?value=success');
      await page.getByRole('button', { name: /重新登录/ }).click();
      await authorize(page);
      await workspace(page);
    }
  });
}

test('会话过期与会话库 503 区分，并支持重试', async ({ page }) => {
  await begin(page);
  await authorize(page);
  await workspace(page);
  await control(page, 'db-outage');
  try {
    expect((await page.request.get('/api/v1/me')).status()).toBe(503);
    await page.reload();
    await expect(page).toHaveURL('/login');
    await expect(page.getByRole('alert')).toContainText('会话服务暂时不可用');
  } finally {
    await control(page, 'db-online');
  }
  await page.getByRole('button', { name: '重试连接' }).click();
  await workspace(page);

  await control(page, 'expire-session');
  await page.getByRole('button', { name: '我的报备', exact: true }).click();
  await expect(page).toHaveURL('/login');
  await expect(page.getByRole('alert')).toContainText('登录会话已过期');
  await page.reload();
  await expect(page.getByRole('alert')).toContainText('登录会话已过期');
  await page.getByRole('button', { name: /重新登录/ }).click();
  await authorize(page);
  await workspace(page);
});

test('回调时会话库不可用，恢复后可重新登录', async ({ page }) => {
  const previous = await metrics(page);
  await begin(page);
  await control(page, 'db-outage');
  try {
    await authorize(page);
    await expect(page).toHaveURL('/login?error=SESSION_UNAVAILABLE');
    await expect(page.getByRole('alert')).toContainText('会话服务暂时不可用');
  } finally {
    await control(page, 'db-online');
  }
  expect(await metrics(page)).toEqual(previous);
  await page.getByRole('button', { name: /重新登录/ }).click();
  await authorize(page);
  await workspace(page);
});

test('登录配置网络失败后重试，未知查询值不回显', async ({ page }) => {
  await page.route('**/auth/config', route => route.abort('internetdisconnected'), { times: 1 });
  await page.goto('/login?error=sensitive-fixture-description');
  await expect(page.getByRole('alert')).toContainText('网络连接失败');
  await expect(page.locator('body')).not.toContainText('sensitive-fixture-description');
  await page.getByRole('button', { name: '重试连接' }).click();
  await expect(page.getByRole('button', { name: /企业账号登录/ })).toBeEnabled();
});
