import { test, expect } from '@playwright/test';
import { login } from './login';

// Gate D: role matrix, project isolation and CSRF are asserted on both surfaces —
// the UI hides the write entry, and the server rejects the same write. Each case
// runs in its own fresh browser context (fresh Keycloak login).

const roleCases = [
  { name: 'viewer 只读', user: 'b4-viewer', createVisible: 0, status: 403 },
  { name: 'operator 不写资产主数据', user: 'b4-operator', createVisible: 0, status: 403 },
  { name: 'maintainer 不写资产主数据', user: 'b4-maintainer', createVisible: 0, status: 403 },
  { name: '零授权用户无写能力', user: 'b4-noauth', createVisible: 0, status: 404 },
  { name: 'platform_admin 可写授权项目', user: 'b4-admin', createVisible: 1, status: 201 },
] as const;

for (const c of roleCases) {
  test(`RBAC：${c.name}（前端隐藏 + 服务端拒绝/放行）`, async ({ page }) => {
    await login(page, c.user);
    await page.goto('/#/assets');
    await expect(page.getByTestId('asset-center')).toBeVisible();
    await expect(page.getByTestId('asset-create')).toHaveCount(c.createVisible);
    const status = await page.evaluate(async () => {
      const s = await (await fetch('/auth/session')).json();
      const res = await fetch('/api/v1/assets', {
        method: 'POST',
        headers: { 'X-CSRF-Token': s.csrfToken, 'Content-Type': 'application/json' },
        body: JSON.stringify({ projectId: 'P001', assetType: 'CRANE', assetCode: 'CRN-RBAC-' + Date.now() }),
      });
      return res.status;
    });
    expect(status).toBe(c.status);
  });
}

test('RBAC：P001 platform_admin 不能越权写 P002', async ({ page }) => {
  await login(page, 'b4-admin');
  const status = await page.evaluate(async () => {
    const s = await (await fetch('/auth/session')).json();
    const res = await fetch('/api/v1/assets', {
      method: 'POST',
      headers: { 'X-CSRF-Token': s.csrfToken, 'Content-Type': 'application/json' },
      body: JSON.stringify({ projectId: 'P002', assetType: 'CRANE', assetCode: 'CRN-XPROJ' }),
    });
    return res.status;
  });
  expect(status).toBe(404);
});

test('RBAC：P002 用户不能越权写 P001', async ({ page }) => {
  await login(page, 'b4-p002');
  const status = await page.evaluate(async () => {
    const s = await (await fetch('/auth/session')).json();
    const res = await fetch('/api/v1/assets', {
      method: 'POST',
      headers: { 'X-CSRF-Token': s.csrfToken, 'Content-Type': 'application/json' },
      body: JSON.stringify({ projectId: 'P001', assetType: 'CRANE', assetCode: 'CRN-XPROJ-2' }),
    });
    return res.status;
  });
  expect(status).toBe(404);
});

test('RBAC：P001 用户直接读取 P002 资产隐藏为 404', async ({ page }) => {
  await login(page, 'b4-admin');
  const status = await page.evaluate(async () => (await fetch('/api/v1/assets/b4-p002-asset')).status);
  expect(status).toBe(404);
});

test('RBAC：P002 用户可读取本项目固定反证资产', async ({ page }) => {
  await login(page, 'b4-p002');
  const status = await page.evaluate(async () => (await fetch('/api/v1/assets/b4-p002-asset')).status);
  expect(status).toBe(200);
});

test('CSRF：缺失或错误 CSRF 不能写入', async ({ page }) => {
  await login(page, 'b4-admin');
  const [missing, wrong] = await page.evaluate(async () => {
    const body = JSON.stringify({ projectId: 'P001', assetType: 'CRANE', assetCode: 'CRN-CSRF-' + Date.now() });
    const missingRes = await fetch('/api/v1/assets', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body });
    const wrongRes = await fetch('/api/v1/assets', { method: 'POST', headers: { 'X-CSRF-Token': 'wrong-token', 'Content-Type': 'application/json' }, body });
    return [missingRes.status, wrongRes.status];
  });
  expect(missing).toBe(403);
  expect(wrong).toBe(403);
});

test('会话：退出后旧会话不可继续写入', async ({ page }) => {
  await login(page, 'b4-admin');
  await page.getByRole('button', { name: '退出', exact: true }).click();
  const confirm = page.locator('#kc-logout');
  await expect(confirm).toBeVisible();
  await confirm.click();
  await page.waitForURL((url) => url.pathname === '/');
  await expect(page.getByRole('link', { name: '账号登录', exact: true })).toBeVisible();
  const status = await page.evaluate(async () => {
    const res = await fetch('/api/v1/assets', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ projectId: 'P001', assetType: 'CRANE', assetCode: 'CRN-LOGOUT' }),
    });
    return res.status;
  });
  expect(status).toBe(401);
});
