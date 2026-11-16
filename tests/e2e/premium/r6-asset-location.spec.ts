// R6-BIZ-R1 in a real browser against a stateful mocked API that follows the server rules:
//   F-02 — an empty equipment type directory shows the administrator hint and never submits;
//   F-03 — the asset detail shows the current installation location (or an honest empty state),
//          refreshes it after a move, opens the history and links to 位置中心.
// The real Keycloak + PostgreSQL proof is tests/e2e/b4/r6-business-closure.spec.ts.
// Locators are scoped to the page region (detail actions / dialogs / sections), never the side rail.
import { test, expect, type Page } from '@playwright/test';

const session = { userId: 'u', displayName: 'admin', projects: [{ projectId: 'P001', projectName: 'Project-A', role: 'platform_admin' }], csrfToken: 'x'.repeat(32), expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' };
const baseAsset = { id: 'A1', projectId: 'P001', displayName: '1 号行车', assetCode: 'C1', assetType: 'CRANE', assetStatus: 'ACTIVE', manufacturerId: null, manufacturerSnapshot: '', brandSnapshot: '', modelSnapshot: '', serialNumber: null, manufactureDate: null, installationDate: null, commissioningDate: null, liftingHeightM: null, applicationType: null, controlMode: null, version: 1, specialEquipmentCode: null, useRegistrationNo: null };
const loc = (id: string, kind: string, name: string, code: string | null, parentId: string | null) => ({ id, projectId: 'P001', parentId, kind, code, name, province: '', city: '', county: '', longitude: null, latitude: null, coordinateSystem: 'UNKNOWN', coordinateSource: 'MANUAL', status: 'ACTIVE', version: 1, address: '' });
const LOCS = [loc('S1', 'SITE', '一号场地', 'S1', null), loc('P1', 'PLANT', '一号厂房', null, 'S1'), loc('W1', 'WORKSHOP', '一车间', 'W1', 'P1'), loc('W2', 'WORKSHOP', '二车间', 'W2', 'P1')];

type Inst = { installationId: string; locationId: string; validFrom: string; validTo: string | null };

async function mock(page: Page, opts: { types?: { typeCode: string; displayName: string }[]; locationStatus?: number } = {}) {
  const state = { asset: { ...baseAsset }, installs: [] as Inst[], equipmentPosts: 0, locationReads: 0 };
  const pathOf = (id: string) => {
    const out = [];
    for (let l = LOCS.find((x) => x.id === id); l; l = LOCS.find((x) => x.id === l!.parentId)) out.unshift({ id: l.id, kind: l.kind, code: l.code, name: l.name, status: l.status });
    return out;
  };
  const record = (i: Inst) => {
    const l = LOCS.find((x) => x.id === i.locationId)!;
    return { installationId: i.installationId, locationId: l.id, code: l.code, name: l.name, kind: l.kind, status: l.status, validFrom: i.validFrom, validTo: i.validTo, current: i.validTo === null, path: pathOf(l.id) };
  };
  await page.route('**/auth/session', (r) => r.fulfill({ json: session }));
  await page.route('**/api/v1/**', async (r) => {
    const u = new URL(r.request().url());
    const m = r.request().method();
    if (u.pathname === '/api/v1/assets/A1') return r.fulfill({ json: state.asset });
    if (u.pathname === '/api/v1/assets/A1/location') {
      state.locationReads++;
      if (opts.locationStatus) return r.fulfill({ status: opts.locationStatus, json: { error: 'database unavailable' } });
      const cur = state.installs.find((i) => i.validTo === null);
      const rec = cur ? record(cur) : null;
      return r.fulfill({ json: { assetId: 'A1', projectId: 'P001', assetStatus: state.asset.assetStatus, current: rec && { installationId: rec.installationId, locationId: rec.locationId, code: rec.code, name: rec.name, kind: rec.kind, status: rec.status, validFrom: rec.validFrom, path: rec.path } } });
    }
    if (u.pathname === '/api/v1/assets/A1/installation-history') {
      const items = [...state.installs].reverse().map(record);
      return r.fulfill({ json: { assetId: 'A1', total: items.length, limit: 20, offset: 0, items } });
    }
    if (u.pathname === '/api/v1/projects/P001/locations') return r.fulfill({ json: LOCS });
    if (u.pathname === '/api/v1/assets/A1/move' && m === 'POST') {
      if (r.request().headers()['if-match'] !== `"${state.asset.version}"`) return r.fulfill({ status: 409, json: { error: 'version conflict' } });
      const { locationId } = r.request().postDataJSON() as { locationId: string };
      const now = new Date(Date.UTC(2026, 10, 16, 2, state.installs.length)).toISOString();
      for (const i of state.installs) if (i.validTo === null) i.validTo = now;
      state.installs.push({ installationId: `I${state.installs.length + 1}`, locationId, validFrom: now, validTo: null });
      state.asset = { ...state.asset, version: state.asset.version + 1 };
      return r.fulfill({ json: { status: 'moved' } });
    }
    if (u.pathname === '/api/v1/equipment/types') return r.fulfill({ json: (opts.types ?? []).map((t) => ({ ...t, domain: 'MECHANICAL', specSchemaVersion: '1', status: 'ACTIVE' })) });
    if (u.pathname === '/api/v1/assets/A1/equipment' && m === 'POST') {
      state.equipmentPosts++;
      return r.fulfill({ status: 422, json: { error: 'invalid input' } });
    }
    return r.fulfill({ json: [] });
  });
  return state;
}

const shots = process.env.R6_EVIDENCE_DIR ?? 'test-results/r6';

test('F-02 类型目录为空：安装表单显示管理员提示，安装按钮禁用，不发出空类型请求', async ({ page }) => {
  const state = await mock(page, { types: [] });
  await page.goto('/#/assets/A1');
  const detail = page.getByTestId('asset-detail');
  await detail.locator('.tabs').getByRole('button', { name: '设备', exact: true }).click();
  await detail.getByRole('button', { name: '安装 Equipment', exact: true }).click();
  const form = page.getByTestId('equipment-create-form');
  await expect(form.getByTestId('equipment-types-empty')).toHaveText('尚未配置可安装的设备类型，请联系管理员初始化类型目录');
  await expect(form.getByLabel('设备类型选择').locator('option')).toHaveCount(1);
  await form.getByLabel('Equipment 编号').fill('EQ-EMPTY');
  await expect(form.getByTestId('equipment-create-submit')).toBeDisabled();
  await form.getByLabel('Equipment 编号').press('Enter');
  await page.waitForTimeout(300);
  expect(state.equipmentPosts).toBe(0);
  await page.screenshot({ path: `${shots}/f02-empty-types.png` });
});

test('F-02 有可用类型时：未选类型不能提交；选中后才可提交', async ({ page }) => {
  const state = await mock(page, { types: [{ typeCode: 'MOTOR', displayName: '电机' }] });
  await page.goto('/#/assets/A1');
  const detail = page.getByTestId('asset-detail');
  await detail.locator('.tabs').getByRole('button', { name: '设备', exact: true }).click();
  await detail.getByRole('button', { name: '安装 Equipment', exact: true }).click();
  const form = page.getByTestId('equipment-create-form');
  await expect(form.getByTestId('equipment-types-empty')).toHaveCount(0);
  await form.getByLabel('Equipment 编号').fill('EQ-1');
  await expect(form.getByTestId('equipment-create-submit')).toBeDisabled();
  await form.getByLabel('设备类型选择').selectOption('MOTOR');
  await expect(form.getByTestId('equipment-create-submit')).toBeEnabled();
  await form.getByTestId('equipment-create-submit').click();
  await expect(form.getByTestId('equipment-create-error')).toBeVisible();
  expect(state.equipmentPosts).toBe(1);
});

test('F-03 当前位置：首次无位置诚实空态 → 移动后刷新为完整路径 → 再移动 → 历史两条 → 跳转位置中心', async ({ page }) => {
  const state = await mock(page);
  await page.goto('/#/assets/A1');
  const section = page.getByTestId('asset-current-location');
  await expect(section.getByTestId('asset-location-empty')).toContainText('该资产尚未安装到任何位置');
  await expect(section).not.toContainText('经度');
  await page.screenshot({ path: `${shots}/f03-empty.png` });

  const actions = page.locator('.detail-actions');
  const moveForm = page.getByTestId('asset-move-form');
  await actions.getByRole('button', { name: '移动', exact: true }).click();
  await moveForm.getByLabel('目标位置').selectOption({ label: '一车间（WORKSHOP）' });
  await moveForm.getByRole('button', { name: '移动', exact: true }).click();
  await expect(moveForm).toBeHidden();
  await expect(section.getByTestId('asset-location-current')).toHaveAttribute('data-location-id', 'W1');
  await expect(section.getByTestId('asset-location-path')).toHaveText('场地·一号场地（S1） › 厂区·一号厂房 › 车间·一车间（W1）');
  await expect(page.getByTestId('asset-moved-location-link')).toBeVisible();

  await actions.getByRole('button', { name: '移动', exact: true }).click();
  await moveForm.getByLabel('目标位置').selectOption({ label: '二车间（WORKSHOP）' });
  await moveForm.getByRole('button', { name: '移动', exact: true }).click();
  await expect(moveForm).toBeHidden();
  await expect(section.getByTestId('asset-location-current')).toHaveAttribute('data-location-id', 'W2');
  await expect(section.getByTestId('asset-location-status')).toHaveText('在用');
  await page.screenshot({ path: `${shots}/f03-current.png` });

  await section.getByTestId('asset-location-history-open').click();
  const history = page.getByTestId('asset-location-history');
  const rows = history.getByTestId('asset-location-history-table').locator('tbody tr');
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toHaveAttribute('data-current', 'true');
  await expect(rows.nth(0)).toContainText('二车间');
  await expect(rows.nth(1)).toHaveAttribute('data-current', 'false');
  await expect(rows.nth(1)).toContainText('一车间');
  await page.screenshot({ path: `${shots}/f03-history.png` });
  await page.keyboard.press('Escape');
  await expect(history).toBeHidden();

  await section.getByTestId('asset-location-link').click();
  await expect(page).toHaveURL(/#\/locations\?project=P001&location=W2/);
  await expect(page.getByTestId('location-table').locator('tr[data-location-id="W2"]')).toHaveAttribute('aria-current', 'location');
  expect(state.installs.filter((i) => i.validTo === null)).toHaveLength(1);
});

test('F-03 位置读取失败：显示错误与重试，而不是“未安装”', async ({ page }) => {
  await mock(page, { locationStatus: 503 });
  await page.goto('/#/assets/A1');
  const section = page.getByTestId('asset-current-location');
  await expect(section.getByTestId('asset-location-error')).toContainText('服务暂不可用');
  await expect(section.getByTestId('asset-location-empty')).toHaveCount(0);
});
