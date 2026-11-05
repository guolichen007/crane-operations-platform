// G3-D2 real-browser guards for the asset editors, against a mocked API (no
// server writes). Complements the real Keycloak + PostgreSQL B4 specs: this one
// runs in the frontend CI job on every push and can inject slow / failing
// responses deterministically.
import { test, expect, type Page } from '@playwright/test';
const session = { userId: 'u', displayName: 'admin', projects: [{ projectId: 'P001', projectName: 'Project-A', role: 'platform_admin' }], csrfToken: 'x', expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' };
const base = { id: 'A1', projectId: 'P001', displayName: '原名', assetCode: 'C1', assetType: 'CRANE', assetStatus: 'DRAFT', manufacturerId: null, manufacturerSnapshot: '', brandSnapshot: '', modelSnapshot: '', serialNumber: null, manufactureDate: null, installationDate: null, commissioningDate: null, liftingHeightM: null, applicationType: null, controlMode: null, version: 1, specialEquipmentCode: null, useRegistrationNo: null };
type Reply = { status: number; json: unknown };
async function mock(page: Page, onPatch: () => Promise<Reply>) {
  let server: unknown = { ...base };
  await page.route('**/auth/session', (r) => r.fulfill({ json: session }));
  await page.route('**/api/v1/**', async (r) => {
    const u = new URL(r.request().url()); const m = r.request().method();
    if (u.pathname === '/api/v1/assets/A1' && m === 'PATCH') { const res = await onPatch(); if (res.status === 200) server = res.json; return r.fulfill(res); }
    if (u.pathname === '/api/v1/assets/A1') return r.fulfill({ json: server });
    if (u.pathname === '/api/v1/assets') return r.fulfill({ json: [server] });
    if (m === 'POST' && u.pathname === '/api/v1/assets/A1/equipment') { await new Promise((x) => setTimeout(x, 1500)); return r.fulfill({ status: 422, json: { error: 'spec invalid' } }); }
    if (u.pathname.startsWith('/api/v1/assets/A1/equipment')) return r.fulfill({ json: [] });
    return r.fulfill({ json: [] });
  });
  return { set: (v: unknown) => (server = v) };
}
test('409 后重基：取消确认草稿与版本不变，确认后保留只读副本，表单外状态变化可见', async ({ page }) => {
  const s = await mock(page, async () => ({ status: 409, json: { error: 'version conflict' } }));
  await page.goto('/#/assets/A1');
  await page.getByRole('button', { name: '编辑', exact: true }).click();
  const form = page.getByTestId('asset-edit-form');
  await form.getByLabel('资产名称').fill('我的草稿');
  s.set({ ...base, displayName: '他人', assetStatus: 'ACTIVE', version: 2 });
  await form.getByRole('button', { name: '保存' }).click();
  await expect(page.getByTestId('asset-conflict-diff')).toContainText('状态');
  await expect(page.getByTestId('asset-conflict-diff')).not.toContainText('version');
  const prompts: string[] = []; let accept = false;
  page.on('dialog', async (d) => { prompts.push(d.message()); accept ? await d.accept() : await d.dismiss(); });
  await page.getByTestId('asset-rebase').click();
  expect(prompts.length).toBe(1);
  await expect(form.getByLabel('资产名称')).toHaveValue('我的草稿');
  await expect(form).toContainText('基于 v1');
  accept = true;
  await page.getByTestId('asset-rebase').click();
  await expect(form).toContainText('基于 v2');
  await expect(form.getByLabel('资产名称')).toHaveValue('他人');
  await expect(page.getByTestId('asset-discarded-draft').getByRole('textbox')).toHaveValue(/资产名称：我的草稿/);
  });
test('资产编辑提交中禁止 Esc/蒙层/取消/切路由，失败后保留输入并原位提示', async ({ page }) => {
  let release!: () => void; const gate = new Promise<void>((r) => (release = r));
  await mock(page, async () => { await gate; return { status: 503, json: { error: 'database unavailable' } }; });
  let prompts = 0; page.on('dialog', async (d) => { prompts++; await d.accept(); });
  await page.goto('/#/assets/A1');
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
  expect(prompts).toBe(0);
  release();
  await expect(page.getByTestId('asset-edit-error')).toContainText('服务暂不可用');
  await expect(form.getByLabel('资产名称')).toHaveValue('提交中的草稿');
});
test('Equipment 安装：干净关闭不提示、有输入需确认、提交中不可关、失败原位提示且保留输入', async ({ page }) => {
  await mock(page, async () => ({ status: 200, json: base }));
  let prompts = 0; let accept = false;
  page.on('dialog', async (d) => { prompts++; accept ? await d.accept() : await d.dismiss(); });
  await page.goto('/#/assets/A1');
  await page.locator('.tabs').getByRole('button', { name: '设备', exact: true }).click();
  const form = page.getByTestId('equipment-create-form');
  await page.getByRole('button', { name: '安装 Equipment' }).click();
  await page.keyboard.press('Escape'); await expect(form).toHaveCount(0);
  await page.getByRole('button', { name: '安装 Equipment' }).click();
  await form.getByRole('button', { name: '取消' }).click(); await expect(form).toHaveCount(0);
  expect(prompts).toBe(0);
  await page.getByRole('button', { name: '安装 Equipment' }).click();
  await form.getByLabel('Equipment 编号').fill('EQ-X');
  await page.keyboard.press('Escape');
  await page.locator('.modal-backdrop').click({ position: { x: 5, y: 5 } });
  await form.getByRole('button', { name: '取消' }).click();
  expect(prompts).toBe(3);
  await form.getByLabel('设备类型选择').selectOption('MOTOR');
  await form.getByRole('button', { name: '安装' }).click();
  await expect(form.getByRole('button', { name: '安装中…' })).toBeDisabled();
  await page.keyboard.press('Escape'); expect(prompts).toBe(3);
  await expect(page.getByTestId('equipment-create-error')).toContainText('输入不符合要求');
  await expect(form.getByLabel('Equipment 编号')).toHaveValue('EQ-X');
  accept = true;
  await form.getByRole('button', { name: '取消' }).click(); expect(prompts).toBe(4); await expect(form).toHaveCount(0);
});
test('新建资产表单：干净关闭不提示，有输入时 Esc/切路由需确认', async ({ page }) => {
  await mock(page, async () => ({ status: 200, json: base }));
  let prompts = 0; page.on('dialog', async (d) => { prompts++; await d.dismiss(); });
  await page.goto('/#/assets');
  await page.getByTestId('asset-create').click();
  await page.keyboard.press('Escape'); await expect(page.getByTestId('asset-create-form')).toHaveCount(0);
  expect(prompts).toBe(0);
  await page.getByTestId('asset-create').click();
  await page.getByTestId('asset-create-form').getByPlaceholder('CRN-001').fill('X');
  await page.keyboard.press('Escape');
  await page.evaluate(() => (location.hash = '#/overview'));
  expect(prompts).toBe(2);
  await expect(page.getByTestId('asset-create-form')).toBeVisible();
});
