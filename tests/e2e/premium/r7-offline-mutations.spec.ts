// R7 F-12: a write attempted while the browser is offline must not be queued and silently sent
// when the network returns (TanStack Query's default networkMode 'online' pauses mutations and
// resumes them automatically). Real browser against a stateful mocked API; offline is the real
// browser network state (context.setOffline), not a mocked response.
import { test, expect, type Page } from '@playwright/test';

const ses = (user: string) => ({ userId: user, displayName: user, projects: [{ projectId: 'P001', projectName: 'Project-A', role: 'platform_admin' }], csrfToken: `${user}-`.padEnd(32, 'x'), expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' });
const asset = { id: 'A1', projectId: 'P001', displayName: '原名', assetCode: 'C1', assetType: 'CRANE', assetStatus: 'ACTIVE', manufacturerId: null, manufacturerSnapshot: '', brandSnapshot: '', modelSnapshot: '', serialNumber: null, manufactureDate: null, installationDate: null, commissioningDate: null, liftingHeightM: null, applicationType: null, controlMode: null, version: 1, specialEquipmentCode: null, useRegistrationNo: null };

type Write = { method: string; path: string; csrf: string | undefined; ifMatch: string | undefined };
async function mock(page: Page, opts: { dropPatchResponse?: boolean } = {}) {
  const state = { user: 'alice', server: { ...asset }, writes: [] as Write[], posts: [] as Write[] };
  await page.route('**/auth/session', (r) => r.fulfill({ json: ses(state.user) }));
  await page.route('**/api/v1/**', async (r) => {
    const u = new URL(r.request().url());
    const m = r.request().method();
    const h = r.request().headers();
    if (m !== 'GET') state.writes.push({ method: m, path: u.pathname, csrf: h['x-csrf-token'], ifMatch: h['if-match'] });
    if (u.pathname === '/api/v1/assets/A1' && m === 'PATCH') {
      if (h['if-match'] !== `"${state.server.version}"`) return r.fulfill({ status: 409, json: { error: 'version conflict' } });
      state.server = { ...state.server, ...(r.request().postDataJSON() as object), version: state.server.version + 1 };
      if (opts.dropPatchResponse) return r.abort('connectionreset'); // applied on the server, response lost
      return r.fulfill({ json: state.server });
    }
    if (u.pathname === '/api/v1/assets/A1') return r.fulfill({ json: state.server });
    if (u.pathname === '/api/v1/assets/A1/location') return r.fulfill({ json: { assetId: 'A1', projectId: 'P001', assetStatus: 'ACTIVE', current: null } });
    if (u.pathname === '/api/v1/assets' && m === 'POST') return r.fulfill({ status: 201, json: { ...asset, id: 'A2' } });
    if (u.pathname === '/api/v1/projects/P001/locations' && m === 'POST') return r.fulfill({ status: 201, json: { id: 'L1' } });
    return r.fulfill({ json: [] });
  });
  return state;
}

async function openEdit(page: Page) {
  await page.goto('/#/assets/A1');
  await page.locator('.detail-actions').getByRole('button', { name: '编辑', exact: true }).click();
  const form = page.getByTestId('asset-edit-form');
  await form.getByLabel('资产名称').fill('离线草稿');
  return form;
}

test('断网保存：不排队、不转圈，恢复网络后不自动提交，草稿保留', async ({ page, context }) => {
  const state = await mock(page);
  const form = await openEdit(page);
  await context.setOffline(true);
  await form.getByRole('button', { name: '保存', exact: true }).click();
  await expect(page.getByTestId('asset-edit-error')).toContainText('未发送');
  await expect(form.getByRole('button', { name: '保存', exact: true })).toBeEnabled();
  await context.setOffline(false);
  await page.waitForTimeout(1500);
  expect(state.writes.filter((w) => w.method === 'PATCH')).toHaveLength(0);
  await expect(form.getByLabel('资产名称')).toHaveValue('离线草稿');
  // An explicit second click is a new, deliberate submit.
  await form.getByRole('button', { name: '保存', exact: true }).click();
  await expect(form).toBeHidden();
  expect(state.writes.filter((w) => w.method === 'PATCH')).toHaveLength(1);
});

test('断网后切换用户再恢复网络：上一身份的写入不会以新身份发出', async ({ page, context }) => {
  const state = await mock(page);
  const form = await openEdit(page);
  await context.setOffline(true);
  await form.getByRole('button', { name: '保存', exact: true }).click();
  state.user = 'bob'; // another identity logs in (another tab / re-login)
  await context.setOffline(false);
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await page.waitForTimeout(1500);
  expect(state.writes).toHaveLength(0);
});

test('已发送但响应丢失：显示结果未知并核对服务器版本，不自动重试', async ({ page }) => {
  const state = await mock(page, { dropPatchResponse: true });
  const form = await openEdit(page);
  await form.getByRole('button', { name: '保存', exact: true }).click();
  const err = page.getByTestId('asset-edit-error');
  await expect(err).toContainText('结果未知');
  await expect(err).toContainText('v2');
  await expect(err).not.toContainText('回滚');
  expect(state.writes.filter((w) => w.method === 'PATCH')).toHaveLength(1);
  // Re-submitting the same draft carries the old If-Match → 409, so it can never apply twice.
  await page.waitForTimeout(500);
  expect(state.server.version).toBe(2);
});

test('核心写操作断网均不排队：新建资产、新建位置', async ({ page, context }) => {
  const state = await mock(page);
  await page.goto('/#/assets?project=P001');
  await page.getByTestId('asset-create').click();
  const create = page.getByTestId('asset-create-form');
  await create.getByLabel('授权项目选择').selectOption('P001');
  await create.getByPlaceholder('CRN-001').fill('OFF-1');
  await context.setOffline(true);
  await create.getByRole('button', { name: '创建', exact: true }).click();
  await expect(create.getByRole('alert')).toContainText('未发送');
  await context.setOffline(false);
  await page.waitForTimeout(1000);
  expect(state.writes).toHaveLength(0);
  page.once('dialog', (d) => void d.accept());
  await create.getByRole('button', { name: '取消' }).click();

  await page.goto('/#/locations?project=P001');
  await page.getByTestId('location-create').click();
  const loc = page.getByTestId('location-create-form');
  await loc.getByLabel('位置类型').selectOption('SITE');
  await loc.getByLabel('位置名称').fill('离线位置');
  await context.setOffline(true);
  await loc.getByTestId('location-create-submit').click();
  await expect(loc.getByTestId('location-create-error')).toContainText('未发送');
  await context.setOffline(false);
  await page.waitForTimeout(1000);
  expect(state.writes).toHaveLength(0);
});
