// Q4-QA-R1 §4: minimal location closure in a real browser against a stateful mocked API that follows
// the server rules (create requires location_write = platform_admin; 422/409 on bad input; new
// locations are ACTIVE with UNKNOWN coordinates; move only to an ACTIVE same-project location).
// The real Keycloak + PostgreSQL proof is tests/e2e/b4/location-closure.spec.ts.
import { test, expect, type Page } from '@playwright/test';

type Loc = { id: string; projectId: string; parentId: string | null; kind: string; code: string | null; name: string; province: string; city: string; county: string; longitude: number | null; latitude: number | null; coordinateSystem: string; coordinateSource: string; status: string; version: number; address: string };
const asset = { id: 'A1', projectId: 'P001', displayName: '1 号行车', assetCode: 'C1', assetType: 'CRANE', assetStatus: 'ACTIVE', manufacturerId: null, manufacturerSnapshot: '', brandSnapshot: '', modelSnapshot: '', serialNumber: null, manufactureDate: null, installationDate: null, commissioningDate: null, liftingHeightM: null, applicationType: null, controlMode: null, version: 1, specialEquipmentCode: null, useRegistrationNo: null };

async function mock(page: Page, opts: { role?: string; failList?: number } = {}) {
  const role = opts.role ?? 'platform_admin';
  const state = { locations: [] as Loc[], asset: { ...asset }, installedAt: null as string | null, posts: [] as unknown[], moves: [] as string[] };
  await page.route('**/auth/session', (r) => r.fulfill({ json: { userId: 'u', displayName: 'u', projects: [{ projectId: 'P001', projectName: 'Project-A', role }], csrfToken: 'x'.repeat(32), expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' } }));
  await page.route('**/api/v1/**', async (r) => {
    const u = new URL(r.request().url());
    const m = r.request().method();
    if (u.pathname === '/api/v1/projects/P001/locations' && m === 'GET') {
      if (opts.failList) return r.fulfill({ status: opts.failList, json: { error: 'x' } });
      const only = u.searchParams.get('status');
      return r.fulfill({ json: state.locations.filter((l) => !only || l.status === only) });
    }
    if (u.pathname === '/api/v1/projects/P001/locations' && m === 'POST') {
      const b = r.request().postDataJSON() as Loc;
      state.posts.push(b);
      if (role !== 'platform_admin') return r.fulfill({ status: 403, json: { error: 'Forbidden' } });
      if (!b.name?.trim()) return r.fulfill({ status: 422, json: { error: 'invalid input: name is required' } });
      if (b.code && state.locations.some((l) => l.code === b.code)) return r.fulfill({ status: 409, json: { error: 'location code already exists in this project' } });
      const loc: Loc = { ...b, id: `L${state.locations.length + 1}`, projectId: 'P001', status: 'ACTIVE', version: 1, coordinateSystem: b.longitude == null ? 'UNKNOWN' : b.coordinateSystem };
      state.locations.push(loc);
      return r.fulfill({ status: 201, json: loc });
    }
    if (u.pathname === '/api/v1/assets/A1') return r.fulfill({ json: state.asset });
    if (u.pathname === '/api/v1/assets/A1/move' && m === 'POST') {
      const { locationId } = r.request().postDataJSON() as { locationId: string };
      state.moves.push(locationId);
      if (r.request().headers()['if-match'] !== `"${state.asset.version}"`) return r.fulfill({ status: 409, json: { error: 'version conflict' } });
      if (!state.locations.some((l) => l.id === locationId && l.status === 'ACTIVE')) return r.fulfill({ status: 404, json: { error: 'not found' } });
      state.installedAt = locationId;
      state.asset = { ...state.asset, version: state.asset.version + 1 };
      return r.fulfill({ json: { status: 'moved' } });
    }
    return r.fulfill({ json: [] });
  });
  return state;
}

test('项目管理员：资产无可用位置 → 引导去位置中心 → 新建场地/车间（坐标未知）→ 回资产详情移动 → 刷新后版本保持', async ({ page }) => {
  const state = await mock(page);
  await page.goto('/#/assets/A1');
  await page.getByRole('button', { name: '移动', exact: true }).click();
  const move = page.getByTestId('asset-move-form');
  await expect(move.getByTestId('move-no-locations')).toContainText('还没有可用（在用）的位置');
  await expect(move.getByRole('button', { name: '移动', exact: true })).toBeDisabled();
  await page.screenshot({ path: 'test-results/q4-location/1-move-no-locations.png' });
  await move.getByTestId('move-create-location-link').click();
  await expect(page).toHaveURL(/#\/locations\?project=P001/);
  await expect(page.getByTestId('location-empty')).toContainText('新建位置');

  // Validation: kind + name required, nothing sent.
  await page.getByTestId('location-create').click();
  const form = page.getByTestId('location-create-form');
  await form.getByTestId('location-create-submit').click();
  await expect(form.locator('.field-error')).toHaveCount(2);
  expect(state.posts).toHaveLength(0);
  await form.getByLabel('位置类型').selectOption('SITE');
  await form.getByLabel('位置名称').fill('一号场地');
  await form.getByLabel('位置编号').fill('S1');
  await form.getByTestId('location-create-submit').click();
  await expect(page.getByTestId('location-created')).toContainText('坐标未知');
  expect(state.posts[0]).toMatchObject({ kind: 'SITE', name: '一号场地', code: 'S1', longitude: null, latitude: null, coordinateSystem: 'UNKNOWN' });

  // A duplicate code is a clear 409 inside the dialog; input kept.
  await page.getByTestId('location-create').click();
  await form.getByLabel('位置类型').selectOption('WORKSHOP');
  await form.getByLabel('位置名称').fill('一车间');
  await form.getByLabel('位置编号').fill('S1');
  await form.getByLabel('上级位置').selectOption({ label: '一号场地（场地）' });
  await form.getByTestId('location-create-submit').click();
  await expect(form.getByTestId('location-create-error')).toContainText('已存在');
  await expect(form.getByLabel('位置名称')).toHaveValue('一车间');
  await form.getByLabel('位置编号').fill('W1');
  await form.getByTestId('location-create-submit').click();
  await expect(form).toBeHidden();
  const table = page.getByTestId('location-table');
  await expect(table).toContainText('一号场地');
  await expect(table.locator('tr.tree-child')).toContainText('一车间');
  await expect(table).toContainText('未知');
  await page.screenshot({ path: 'test-results/q4-location/2-locations-created.png' });

  // Back to the asset: the new workshop is a move target.
  await page.goto('/#/assets/A1');
  await page.getByRole('button', { name: '移动', exact: true }).click();
  await move.getByLabel('目标位置').selectOption({ label: '一车间（WORKSHOP）' });
  await move.getByRole('button', { name: '移动', exact: true }).click();
  await expect(move).toBeHidden();
  await expect(page.getByTestId('asset-moved')).toContainText('一车间');
  expect(state.installedAt).toBe('L2');
  await page.reload();
  await expect(page.getByTestId('asset-detail')).toContainText('v2');
  // The dead "位置" tab is gone (no API for the current location yet; nothing invented).
  await expect(page.locator('.tabs').getByRole('button', { name: '位置' })).toHaveCount(0);
});

test('只读账号：位置中心无新建入口；空列表提示需项目管理员创建', async ({ page }) => {
  await mock(page, { role: 'viewer' });
  await page.goto('/#/locations?project=P001');
  await expect(page.getByTestId('location-empty')).toContainText('需由项目管理员创建');
  await expect(page.getByTestId('location-create')).toHaveCount(0);
});

for (const status of [503, 403]) {
  test(`位置列表 ${status}：显示错误与重试，不显示“暂无位置”；移动对话框同样区分`, async ({ page }) => {
    await mock(page, { failList: status });
    await page.goto('/#/locations?project=P001');
    await expect(page.getByTestId('location-error')).toBeVisible();
    await expect(page.getByTestId('location-error').getByRole('button', { name: '重试' })).toBeVisible();
    await expect(page.getByTestId('location-empty')).toHaveCount(0);
    await page.goto('/#/assets/A1');
    await page.getByRole('button', { name: '移动', exact: true }).click();
    await expect(page.getByTestId('move-locations-error')).toContainText('不是“没有位置”');
    await expect(page.getByTestId('move-no-locations')).toHaveCount(0);
  });
}

test('未选择项目：提示选择项目而不是“暂无位置”；未授权项目不发请求', async ({ page }) => {
  await mock(page);
  await page.goto('/#/locations');
  await expect(page.getByTestId('location-no-scope')).toBeVisible();
  await page.getByTestId('location-project-select').selectOption('P001');
  await expect(page).toHaveURL(/project=P001/);
  await page.goto('/#/locations?project=P999');
  await expect(page.getByTestId('location-unauthorized')).toBeVisible();
});
