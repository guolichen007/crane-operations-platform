// Q4-QA-R1a §2: the asset detail component is REUSED when the route changes from /assets/A to
// /assets/B (same route record, only params change). A draft / confirmation / in-flight submit that
// was opened for A must never be sent for B. Real browser against a stateful mocked API that
// enforces If-Match per asset like the server; records which asset id each write hit.
import { test, expect, type Page } from '@playwright/test';

const session = { userId: 'u', displayName: 'admin', projects: [{ projectId: 'P001', projectName: 'Project-A', role: 'platform_admin' }], csrfToken: 'x'.repeat(32), expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' };
const asset = (id: string, name: string, version: number) => ({ id, projectId: 'P001', displayName: name, assetCode: `C-${id}`, assetType: 'CRANE', assetStatus: 'ACTIVE', manufacturerId: null, manufacturerSnapshot: '', brandSnapshot: '', modelSnapshot: '', serialNumber: null, manufactureDate: null, installationDate: null, commissioningDate: null, liftingHeightM: null, applicationType: null, controlMode: null, version, specialEquipmentCode: null, useRegistrationNo: null });

type Write = { method: string; path: string; ifMatch: string | null; body: unknown };
async function mock(page: Page, opts: { holdWrites?: boolean } = {}) {
  const state = {
    assets: { A: asset('A', 'A 号行车', 3), B: asset('B', 'B 号行车', 3) } as Record<string, ReturnType<typeof asset>>,
    writes: [] as Write[],
    release: () => {},
  };
  let gate: Promise<void> | null = opts.holdWrites ? new Promise<void>((r) => (state.release = r)) : null;
  await page.route('**/auth/session', (r) => r.fulfill({ json: session }));
  await page.route('**/api/v1/**', async (r) => {
    const u = new URL(r.request().url());
    const m = r.request().method();
    const p = u.pathname.replace('/api/v1/', '');
    const parts = p.split('/');
    if (m !== 'GET') {
      state.writes.push({ method: m, path: p, ifMatch: r.request().headers()['if-match'] ?? null, body: r.request().postDataJSON() });
      if (gate) { await gate; gate = null; }
    }
    if (parts[0] === 'assets' && parts.length === 2 && (parts[1] === 'A' || parts[1] === 'B')) {
      const a = state.assets[parts[1]];
      if (m === 'GET') return r.fulfill({ json: a });
      if (m === 'PATCH') {
        if (r.request().headers()['if-match'] !== `"${a.version}"`) return r.fulfill({ status: 409, json: { error: 'version conflict' } });
        state.assets[parts[1]] = { ...a, ...(r.request().postDataJSON() as object), version: a.version + 1 };
        return r.fulfill({ json: state.assets[parts[1]] });
      }
    }
    if (parts[0] === 'assets' && parts.length === 3 && (parts[2] === 'move' || parts[2] === 'retire')) {
      const a = state.assets[parts[1]];
      if (r.request().headers()['if-match'] !== `"${a.version}"`) return r.fulfill({ status: 409, json: { error: 'version conflict' } });
      state.assets[parts[1]] = { ...a, version: a.version + 1, assetStatus: parts[2] === 'retire' ? 'RETIRED' : a.assetStatus };
      return r.fulfill({ json: { status: parts[2] === 'move' ? 'moved' : 'retired' } });
    }
    if (p === 'assets/A/equipment' || p === 'assets/B/equipment') {
      if (m === 'POST') return r.fulfill({ status: 201, json: { id: `EQ-${parts[1]}`, assetId: parts[1], equipmentCode: 'X', typeCode: 'MOTOR', catalogItemId: null, serialNumber: null, semanticSlot: null, positionLabel: null, criticality: 'MEDIUM', administrativeState: 'IN_SERVICE', version: 1, removedAt: null } });
      return r.fulfill({ json: [] });
    }
    if (p === 'projects/P001/locations') return r.fulfill({ json: [{ id: 'L1', projectId: 'P001', parentId: null, kind: 'SITE', code: 'S1', name: '一号场地', province: '', city: '', county: '', longitude: null, latitude: null, coordinateSystem: 'UNKNOWN', coordinateSource: 'MANUAL', status: 'ACTIVE', version: 1, address: '' }] });
    if (p === 'equipment/types') return r.fulfill({ json: [{ typeCode: 'MOTOR', displayName: '电机', domain: 'MECHANICAL', specSchemaVersion: '1', status: 'ACTIVE' }] });
    return r.fulfill({ json: [] });
  });
  return state;
}
/** Same component, new params: exactly what a deep link, global search or browser history does. */
async function gotoB(page: Page) {
  await page.evaluate(() => (location.hash = '#/assets/B'));
}
const detail = (page: Page) => page.getByTestId('asset-detail');
const noWritesTo = (s: { writes: Write[] }, id: string) => s.writes.filter((w) => w.path.startsWith(`assets/${id}`)).length;

test('编辑草稿：A 的草稿在路由更新到 B 时需要确认；拒绝则留在 A，确认放弃后 B 页面无 A 的表单，B 从未被写入', async ({ page }) => {
  const state = await mock(page);
  await page.goto('/#/assets/A');
  await expect(detail(page)).toHaveAttribute('data-asset-id', 'A');
  await page.getByRole('button', { name: '编辑', exact: true }).click();
  const form = page.getByTestId('asset-edit-form');
  await form.getByLabel('资产名称').fill('A 的草稿名称');
  const prompts: string[] = [];
  let accept = false;
  page.on('dialog', async (d) => { prompts.push(d.message()); accept ? await d.accept() : await d.dismiss(); });
  await gotoB(page);
  // Refused: still A, draft intact.
  await expect(detail(page)).toHaveAttribute('data-asset-id', 'A');
  await expect(form.getByLabel('资产名称')).toHaveValue('A 的草稿名称');
  expect(prompts.length).toBeGreaterThan(0);
  // Accept discarding: now B, no editor, nothing written anywhere.
  accept = true;
  await gotoB(page);
  await expect(detail(page)).toHaveAttribute('data-asset-id', 'B');
  await expect(detail(page)).toContainText('B 号行车');
  await expect(page.getByTestId('asset-edit-form')).toHaveCount(0);
  expect(state.writes).toEqual([]);
  expect(state.assets.B.displayName).toBe('B 号行车');
});

for (const [name, open, submitName] of [
  ['移动', async (p: Page) => { await p.getByRole('button', { name: '移动', exact: true }).click(); await p.getByTestId('asset-move-form').getByLabel('目标位置').selectOption('L1'); }, '移动'],
  ['退役', async (p: Page) => { await p.getByRole('button', { name: '退役', exact: true }).click(); }, '确认退役'],
] as const) {
  test(`已打开的${name}对话框：路由更新到 B 后对话框不得残留并对 B 生效`, async ({ page }) => {
    const state = await mock(page);
    page.on('dialog', (d) => void d.accept());
    await page.goto('/#/assets/A');
    await open(page);
    await gotoB(page);
    await expect(detail(page)).toHaveAttribute('data-asset-id', 'B');
    // Either the dialog is gone, or — if it is still there — submitting it must not touch B.
    const btn = page.locator('.workspace-modal').getByRole('button', { name: submitName, exact: true });
    if (await btn.count()) await btn.click().catch(() => undefined);
    await page.waitForTimeout(800);
    expect(noWritesTo(state, 'B')).toBe(0);
    expect(state.assets.B.version).toBe(3);
    await expect(page.locator('.workspace-modal')).toHaveCount(0);
  });
}

test('Equipment 安装表单：A 的安装草稿不能安装到 B', async ({ page }) => {
  const state = await mock(page);
  let accept = false;
  page.on('dialog', async (d) => (accept ? d.accept() : d.dismiss()));
  await page.goto('/#/assets/A');
  await page.locator('.tabs').getByRole('button', { name: '设备', exact: true }).click();
  await page.getByRole('button', { name: '安装 Equipment' }).click();
  const form = page.getByTestId('equipment-create-form');
  await form.getByLabel('设备类型选择').selectOption('MOTOR');
  await form.getByLabel('Equipment 编号').fill('A-MOTOR');
  await gotoB(page);
  await expect(detail(page)).toHaveAttribute('data-asset-id', 'A');
  accept = true;
  await gotoB(page);
  await expect(detail(page)).toHaveAttribute('data-asset-id', 'B');
  await expect(page.getByTestId('equipment-create-form')).toHaveCount(0);
  expect(noWritesTo(state, 'B')).toBe(0);
});

test('提交中切换：A 的保存请求未返回时拒绝切到 B；完成后 A 被更新、B 不变', async ({ page }) => {
  const state = await mock(page, { holdWrites: true });
  let prompts = 0;
  page.on('dialog', async (d) => { prompts++; await d.accept(); });
  await page.goto('/#/assets/A');
  await page.getByRole('button', { name: '编辑', exact: true }).click();
  const form = page.getByTestId('asset-edit-form');
  await form.getByLabel('资产名称').fill('A 的新名称');
  await form.getByRole('button', { name: '保存' }).click();
  await expect(form.getByRole('button', { name: '保存中…' })).toBeDisabled();
  await gotoB(page);
  await expect(detail(page)).toHaveAttribute('data-asset-id', 'A');
  expect(prompts).toBe(0); // refused outright, not a discard prompt
  state.release();
  await expect(form).toBeHidden();
  expect(state.writes.map((w) => w.path)).toEqual(['assets/A']);
  expect(state.assets.A).toMatchObject({ displayName: 'A 的新名称', version: 4 });
  expect(state.assets.B).toMatchObject({ displayName: 'B 号行车', version: 3 });
  await gotoB(page);
  await expect(detail(page)).toHaveAttribute('data-asset-id', 'B');
});

test('干净状态下切换：没有草稿与对话框时 A→B 直接切换且不提示', async ({ page }) => {
  await mock(page);
  let prompts = 0;
  page.on('dialog', async (d) => { prompts++; await d.accept(); });
  await page.goto('/#/assets/A');
  await expect(detail(page)).toContainText('A 号行车');
  await gotoB(page);
  await expect(detail(page)).toContainText('B 号行车');
  expect(prompts).toBe(0);
});
