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

  // 9. 查看已安装设备：类型显示受控字典中文名 + 稳定编码。
  await expect(page.locator('table.data-table')).toContainText('电机（MOTOR）');

  // 10. 修改 Equipment 关键度。
  await page.locator('table.data-table').getByRole('button', { name: '编辑' }).click();
  const eqEditForm = page.getByTestId('equipment-edit-form');
  await eqEditForm.getByLabel('关键度').selectOption('HIGH');
  await eqEditForm.getByRole('button', { name: '保存' }).click();
  await expect(eqEditForm).toHaveCount(0);
  await expect(page.locator('table.data-table tbody tr').first().locator('td').nth(2)).toHaveText('高');

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

// G3-A: the editor pins the version it opened at. Another client advances the
// asset in the background; saving the stale draft must send the pinned If-Match
// and get 409 — the dialog stays open, the draft is intact, the server copy is
// shown for comparison, and the other client's change is not overwritten.
test('编辑固定打开时版本：后台被他人更新后保存得到 409，草稿保留且不覆盖', async ({ page }) => {
  await login(page, 'b4-admin');
  await assertWriteTarget(page);
  const csrf = await sessionCsrf(page);
  const created = await page.evaluate(async ({ csrf, code }) => {
    const res = await fetch('/api/v1/assets', {
      method: 'POST',
      headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' },
      body: JSON.stringify({ projectId: 'P001', assetType: 'CRANE', assetCode: code, displayName: '冲突测试' }),
    });
    return (await res.json()) as { id: string; version: number };
  }, { csrf, code: code('CRN', 'PIN') });

  await page.goto(`/#/assets/${created.id}`);
  await page.getByRole('button', { name: '编辑', exact: true }).click();
  const form = page.getByTestId('asset-edit-form');
  await expect(form).toContainText(`基于 v${created.version}`);
  await form.getByLabel('资产名称').fill('我的旧草稿');

  // Another client updates the asset (v → v+1) while the editor is open.
  const other = await page.evaluate(async ({ id, csrf, version }) => {
    const res = await fetch(`/api/v1/assets/${id}`, {
      method: 'PATCH',
      headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json', 'If-Match': `"${version}"` },
      body: JSON.stringify({ displayName: '他人的修改' }),
    });
    return res.status;
  }, { id: created.id, csrf, version: created.version });
  expect(other).toBe(200);

  // Capture the If-Match the UI sends on save.
  const patch = page.waitForRequest((r) => r.method() === 'PATCH' && r.url().includes(`/api/v1/assets/${created.id}`));
  await form.getByRole('button', { name: '保存' }).click();
  expect((await patch).headers()['if-match']).toContain(String(created.version));

  await expect(page.getByTestId('asset-edit-error')).toContainText('数据已被其他用户修改');
  await expect(form.getByLabel('资产名称')).toHaveValue('我的旧草稿'); // draft kept
  await expect(page.getByTestId('asset-conflict-diff')).toContainText('他人的修改'); // server copy shown

  const stored = await page.evaluate(async (id) => (await (await fetch(`/api/v1/assets/${id}`)).json()) as { displayName: string; version: number }, created.id);
  expect(stored.displayName).toBe('他人的修改'); // not overwritten
  expect(stored.version).toBe(created.version + 1);
});

// G3-A DirtyGuard: a clean editor closes without a prompt; a dirty one asks, and
// dismissing the prompt keeps the dialog open.
test('DirtyGuard：未改动关闭不提示；有改动关闭需确认', async ({ page }) => {
  await login(page, 'b4-admin');
  await assertWriteTarget(page);
  const csrf = await sessionCsrf(page);
  const created = await page.evaluate(async ({ csrf, code }) => {
    const res = await fetch('/api/v1/assets', {
      method: 'POST',
      headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' },
      body: JSON.stringify({ projectId: 'P001', assetType: 'CRANE', assetCode: code }),
    });
    return (await res.json()) as { id: string };
  }, { csrf, code: code('CRN', 'DIRTY') });
  await page.goto(`/#/assets/${created.id}`);

  let prompts = 0;
  page.on('dialog', async (d) => {
    prompts += 1;
    await d.dismiss(); // keep editing
  });

  await page.getByRole('button', { name: '编辑', exact: true }).click();
  await page.keyboard.press('Escape');
  await expect(page.getByTestId('asset-edit-form')).toHaveCount(0);
  expect(prompts).toBe(0);

  await page.getByRole('button', { name: '编辑', exact: true }).click();
  await page.getByTestId('asset-edit-form').getByLabel('资产编号').fill('changed');
  await page.keyboard.press('Escape');
  expect(prompts).toBe(1);
  await expect(page.getByTestId('asset-edit-form')).toBeVisible(); // dismissed → still open
  await page.getByTestId('asset-edit-form').getByRole('button', { name: '取消' }).click();
  expect(prompts).toBe(2);
  await expect(page.getByTestId('asset-edit-form')).toBeVisible();
});

async function createViaApi(page: import('@playwright/test').Page, label: string, extra: Record<string, unknown> = {}) {
  const csrf = await sessionCsrf(page);
  return page.evaluate(async ({ csrf, body }) => {
    const res = await fetch('/api/v1/assets', {
      method: 'POST',
      headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    return (await res.json()) as { id: string; version: number };
  }, { csrf, body: { projectId: 'P001', assetType: 'CRANE', assetCode: code('CRN', label), ...extra } });
}

// G3-D2: after a 409, "start over from the latest version" never replaces a dirty
// draft silently. Cancelling the confirmation leaves the draft and the pinned
// version untouched; confirming keeps a read-only copy of the replaced draft.
test('409 后重基到最新版本：取消确认草稿不变，确认后保留只读副本', async ({ page }) => {
  await login(page, 'b4-admin');
  await assertWriteTarget(page);
  const csrf = await sessionCsrf(page);
  const created = await createViaApi(page, 'REBASE', { displayName: '重基测试' });
  await page.goto(`/#/assets/${created.id}`);
  await page.getByRole('button', { name: '编辑', exact: true }).click();
  const form = page.getByTestId('asset-edit-form');
  await form.getByLabel('资产名称').fill('我的草稿');
  // Another client changes the name AND the status (a field outside the form).
  const other = await page.evaluate(async ({ id, csrf, version }) => {
    const res = await fetch(`/api/v1/assets/${id}`, {
      method: 'PATCH',
      headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json', 'If-Match': `"${version}"` },
      body: JSON.stringify({ displayName: '他人的名称', assetStatus: 'ACTIVE' }),
    });
    return res.status;
  }, { id: created.id, csrf, version: created.version });
  expect(other).toBe(200);
  await form.getByRole('button', { name: '保存' }).click();
  await expect(page.getByTestId('asset-edit-error')).toContainText('数据已被其他用户修改');
  // The status change outside the form is listed, not hidden.
  await expect(page.getByTestId('asset-conflict-diff')).toContainText('状态');

  const prompts: string[] = [];
  let accept = false;
  page.on('dialog', async (d) => {
    prompts.push(d.message());
    if (accept) await d.accept();
    else await d.dismiss();
  });
  await page.getByTestId('asset-rebase').click();
  expect(prompts).toHaveLength(1);
  await expect(form.getByLabel('资产名称')).toHaveValue('我的草稿');
  await expect(form).toContainText(`基于 v${created.version}`);
  await expect(page.getByTestId('asset-discarded-draft')).toHaveCount(0);

  accept = true;
  await page.getByTestId('asset-rebase').click();
  expect(prompts).toHaveLength(2);
  await expect(form).toContainText(`基于 v${created.version + 1}`);
  await expect(form.getByLabel('资产名称')).toHaveValue('他人的名称');
  await expect(page.getByTestId('asset-discarded-draft').getByRole('textbox')).toHaveValue(/资产名称：我的草稿/);
});

// G3-D2: while a save is in flight the dialog cannot be closed (Esc, backdrop,
// cancel, route change); when the backend fails, every input is kept and the
// error is shown in the dialog. The failure is injected by intercepting the PATCH
// in the browser (no request reaches the server).
test('提交期间不可关窗与离开，后端失败后保留输入并原位提示', async ({ page }) => {
  await login(page, 'b4-admin');
  await assertWriteTarget(page);
  const created = await createViaApi(page, 'PENDING', { displayName: '提交中测试' });
  await page.goto(`/#/assets/${created.id}`);
  let release!: () => void;
  const gate = new Promise<void>((r) => (release = r));
  await page.route(`**/api/v1/assets/${created.id}`, async (route) => {
    if (route.request().method() !== 'PATCH') return route.fallback();
    await gate;
    await route.fulfill({ status: 503, json: { error: 'database unavailable' } });
  });
  let prompts = 0;
  page.on('dialog', async (d) => {
    prompts += 1;
    await d.accept();
  });
  await page.getByRole('button', { name: '编辑', exact: true }).click();
  const form = page.getByTestId('asset-edit-form');
  await form.getByLabel('资产名称').fill('提交中的草稿');
  await form.getByRole('button', { name: '保存' }).click();
  await expect(form.getByRole('button', { name: '保存中…' })).toBeDisabled();
  await expect(form.getByRole('button', { name: '取消' })).toBeDisabled();
  await page.keyboard.press('Escape');
  await page.locator('.modal-backdrop').click({ position: { x: 5, y: 5 } });
  await page.evaluate(() => (location.hash = '#/assets'));
  await expect(form).toBeVisible();
  await expect(page.getByTestId('asset-detail')).toBeVisible();
  expect(prompts).toBe(0); // refused outright, not via a discard prompt
  release();
  await expect(page.getByTestId('asset-edit-error')).toContainText('服务暂不可用');
  await expect(form.getByLabel('资产名称')).toHaveValue('提交中的草稿');
  await expect(form.getByRole('button', { name: '保存' })).toBeEnabled();
  // Nothing was written.
  const stored = await page.evaluate(async (id) => (await (await fetch(`/api/v1/assets/${id}`)).json()) as { displayName: string; version: number }, created.id);
  expect(stored).toMatchObject({ displayName: '提交中测试', version: created.version });
});

// G3-D2: the Equipment install form is under the same DirtyGuard — clean closes
// silently; dirty asks on Esc / backdrop / cancel and dismissing keeps it open.
test('Equipment 安装表单 DirtyGuard：干净关闭不提示，有输入需确认', async ({ page }) => {
  await login(page, 'b4-admin');
  await assertWriteTarget(page);
  const created = await createViaApi(page, 'EQDIRTY');
  await page.goto(`/#/assets/${created.id}`);
  await page.locator('.tabs').getByRole('button', { name: '设备', exact: true }).click();
  let prompts = 0;
  let accept = false;
  page.on('dialog', async (d) => {
    prompts += 1;
    if (accept) await d.accept();
    else await d.dismiss();
  });
  const form = page.getByTestId('equipment-create-form');
  await page.getByRole('button', { name: '安装 Equipment' }).click();
  await page.keyboard.press('Escape');
  await expect(form).toHaveCount(0);
  await page.getByRole('button', { name: '安装 Equipment' }).click();
  await form.getByRole('button', { name: '取消' }).click();
  await expect(form).toHaveCount(0);
  expect(prompts).toBe(0);

  await page.getByRole('button', { name: '安装 Equipment' }).click();
  await form.getByLabel('Equipment 编号').fill(code('EQ', 'DIRTY'));
  await page.keyboard.press('Escape');
  await page.locator('.modal-backdrop').click({ position: { x: 5, y: 5 } });
  await form.getByRole('button', { name: '取消' }).click();
  expect(prompts).toBe(3);
  await expect(form.getByLabel('Equipment 编号')).toHaveValue(code('EQ', 'DIRTY'));
  accept = true;
  await form.getByRole('button', { name: '取消' }).click();
  expect(prompts).toBe(4);
  await expect(form).toHaveCount(0);
});
