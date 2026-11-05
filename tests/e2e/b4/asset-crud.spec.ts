import { test, expect } from '@playwright/test';
import { login, sessionCsrf } from './login';
import { code, assertWriteTarget } from './run';

const assetCode = code('CRN', 'CRUD');
const equipmentCode = code('EQ', 'CRUD');

// Gate C: the full asset + equipment business lifecycle is driven by real UI
// button/form interaction (not API-only). The authoritative PostgreSQL terminal
// state is asserted by the fixture's `verify-b4-state` operation after this spec.
test('真实 Keycloak 浏览器：资产与 Equipment 完整业务 CRUD（新建→改→移→装→改→拆→退役）', async ({ page }) => {
  await login(page, 'b4-admin');
  await assertWriteTarget(page);

  // 1-2. 资产管理中心，选择 P001 项目并新建资产。
  await page.goto('/#/assets');
  await expect(page.getByTestId('asset-center')).toBeVisible();
  await page.getByTestId('asset-create').click();
  const createForm = page.getByTestId('asset-create-form');
  await createForm.getByLabel('授权项目选择').selectOption('P001');
  await createForm.getByPlaceholder('CRN-001').fill(assetCode);
  await createForm.getByRole('button', { name: '创建' }).click();

  // 3. 进入资产详情（版本 v1；权威版本终态由 verify-b4-state 从 PG 断言）。
  await expect(page.getByTestId('asset-detail')).toBeVisible();
  await expect(page.getByTestId('asset-detail')).toContainText(assetCode);
  await expect(page.getByTestId('asset-detail')).toContainText('v1');
  const assetId = new URL(page.url()).hash.replace('#/assets/', '');

  // 4. 修改资产：改类型（保留编号供 PG 终态核对），版本递增到 v2。
  await page.getByRole('button', { name: '编辑', exact: true }).click();
  const editForm = page.locator('.workspace-modal');
  await editForm.getByLabel('类型').fill('GANTRY');
  await editForm.getByRole('button', { name: '保存' }).click();
  await expect(page.getByTestId('asset-detail')).toContainText('GANTRY');
  await expect(page.getByTestId('asset-detail')).toContainText('v2');

  // 5-7. 选择合法位置并移动（P001 已由 fixture 种入 ACTIVE 位置）。
  await page.getByRole('button', { name: '移动', exact: true }).click();
  const moveForm = page.locator('.workspace-modal');
  await moveForm.locator('select').selectOption({ index: 1 });
  await moveForm.getByRole('button', { name: '移动', exact: true }).click();
  await expect(page.getByTestId('asset-detail')).toBeVisible();

  // 8. 安装 MOTOR Equipment。
  await page.locator('.tabs').getByRole('button', { name: '设备', exact: true }).click();
  await page.getByRole('button', { name: '安装 Equipment' }).click();
  const eqForm = page.getByTestId('equipment-create-form');
  await eqForm.getByLabel('设备类型选择').selectOption('MOTOR');
  await eqForm.getByLabel('Equipment 编号').fill(equipmentCode);
  await eqForm.getByRole('button', { name: '安装' }).click();
  await expect(page.locator('table.data-table')).toContainText(equipmentCode);

  // 9. 查看已安装设备。
  await expect(page.locator('table.data-table')).toContainText('MOTOR');

  // 10. 修改 Equipment 关键度。
  await page.locator('table.data-table').getByRole('button', { name: '编辑' }).click();
  const eqEditForm = page.getByTestId('equipment-edit-form');
  await eqEditForm.getByLabel('关键度').selectOption('HIGH');
  await eqEditForm.getByRole('button', { name: '保存' }).click();
  await expect(page.locator('table.data-table')).toContainText('HIGH');

  // 12. 拆卸 Equipment（软拆卸，保留历史）。
  await page.locator('table.data-table').getByRole('button', { name: '拆卸' }).click();
  await page.getByRole('button', { name: '确认拆卸' }).click();
  // 13. 已拆卸设备留在历史中，且不能再编辑/拆卸。
  await expect(page.locator('table.data-table')).toContainText('已拆卸');
  await expect(page.locator('table.data-table').getByRole('button', { name: '编辑' })).toHaveCount(0);
  await expect(page.locator('table.data-table').getByRole('button', { name: '拆卸' })).toHaveCount(0);

  // 14. 退役资产（终态保护）。
  await page.getByRole('button', { name: '退役', exact: true }).click();
  await page.getByRole('button', { name: '确认退役' }).click();
  await expect(page.getByTestId('asset-detail')).toContainText('退役');
  // 终态资产不可再编辑/移动。
  await expect(page.getByRole('button', { name: '编辑', exact: true })).toHaveCount(0);

  // 15. 终端状态由 fixture 的 verify-b4-state 从 PostgreSQL 断言（assetId 固定码可追溯）。
  expect(assetId).not.toBe('');
});

// Gate C step 11: two sequential updates that both carry the same (now stale)
// If-Match version — the first succeeds, the second is a 409. This proves stale
// version rejection, not true concurrent submission (the Go concurrency tests
// cover real races). It runs in the authenticated browser context (session
// cookie + CSRF).
test('If-Match 旧版本冲突：同一版本号顺序提交两次，第二次 409', async ({ page }) => {
  await login(page, 'b4-admin');
  await assertWriteTarget(page);
  const csrf = await sessionCsrf(page);
  const codes = { base: code('CRN', '409'), a: code('CRN', '409A'), b: code('CRN', '409B') };
  const created = await page.evaluate(async ({ csrf, codes }) => {
    const res = await fetch('/api/v1/assets', {
      method: 'POST',
      headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' },
      body: JSON.stringify({ projectId: 'P001', assetType: 'CRANE', assetCode: codes.base }),
    });
    return (await res.json()) as { id: string; version: number };
  }, { csrf, codes });

  const results = await page.evaluate(async ({ id, csrf, codes }) => {
    const headers = { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json', 'If-Match': '"1"' };
    const first = await fetch(`/api/v1/assets/${id}`, {
      method: 'PATCH',
      headers,
      body: JSON.stringify({ assetCode: codes.a }),
    });
    const second = await fetch(`/api/v1/assets/${id}`, {
      method: 'PATCH',
      headers,
      body: JSON.stringify({ assetCode: codes.b }),
    });
    return { first: first.status, second: second.status };
  }, { id: created.id, csrf, codes });

  expect(results.first).toBe(200);
  expect(results.second).toBe(409);
});
