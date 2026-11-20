import { test, expect, type Page } from '@playwright/test';
import { login, sessionCsrf } from './login';
import { code, assertWriteTarget, createAsset } from './run';

// R6-BIZ-R1 on the real Keycloak + BFF + PostgreSQL fixture. tools/a1-production-test.mjs runs the
// @r6-empty-types test FIRST, on the freshly migrated schema-12 database whose equipment type
// directory is empty (the fixture no longer seeds it), then initializes MOTOR/PLC with the
// production `equipment-types` binary (plan → approved apply → verify), then runs the rest.
// Locators are scoped to the page region (detail header actions, dialogs, sections) — never the
// side rail, whose entries share words with business buttons.

const detail = (page: Page) => page.getByTestId('asset-detail');
const actions = (page: Page) => detail(page).locator('.detail-actions');
const section = (page: Page) => page.getByTestId('asset-current-location');

async function apiJSON(page: Page, method: string, path: string, csrf: string, body?: unknown, ifMatch?: number) {
  return page.evaluate(async ({ method, path, csrf, body, ifMatch }) => {
    const headers: Record<string, string> = { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' };
    if (ifMatch !== undefined) headers['If-Match'] = String(ifMatch);
    const res = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
    const text = await res.text();
    let json: unknown = null;
    try { json = JSON.parse(text); } catch { json = null; }
    return { status: res.status, json: json as Record<string, unknown> | null };
  }, { method, path, csrf, body, ifMatch });
}

test('@r6-empty-types 全新 schema12 类型目录为空：安装入口提示管理员初始化，API 直接安装 422', async ({ page }) => {
  await login(page, 'b4-admin');
  await assertWriteTarget(page);
  const csrf = await sessionCsrf(page);
  const types = await apiJSON(page, 'GET', '/api/v1/equipment/types', csrf);
  expect(types.status).toBe(200);
  expect(types.json).toEqual([]);
  const a = await createAsset(page, csrf, { projectId: 'P001', assetType: 'CRANE', assetCode: code('CRN', 'R6-EMPTY') });
  await page.goto(`/#/assets/${a.id}`);
  await detail(page).locator('.tabs').getByRole('button', { name: '设备', exact: true }).click();
  await detail(page).getByRole('button', { name: '安装器件', exact: true }).click();
  const form = page.getByTestId('equipment-create-form');
  await expect(form.getByTestId('equipment-types-empty')).toHaveText('尚未配置可安装的设备类型，请联系管理员初始化类型目录');
  await form.getByLabel('器件编号').fill(code('EQ', 'R6-EMPTY'));
  await expect(form.getByTestId('equipment-create-submit')).toBeDisabled();
  await page.screenshot({ path: 'test-results-r6/r6-empty-types.png' });
  const direct = await apiJSON(page, 'POST', `/api/v1/assets/${a.id}/equipment`, csrf, { equipmentCode: code('EQ', 'R6-DIRECT'), typeCode: 'MOTOR', criticality: 'MEDIUM', administrativeState: 'IN_SERVICE', spec: {} });
  expect(direct.status).toBe(422);
});

test('R6 初始化后：资产当前位置空态 → 界面移动 → 完整路径与历史 → 位置中心定位 → 安装器件', async ({ page }) => {
  await login(page, 'b4-admin');
  await assertWriteTarget(page);
  const csrf = await sessionCsrf(page);
  const assetCode = code('CRN', 'R6-LOC');
  const a = await createAsset(page, csrf, { projectId: 'P001', assetType: 'CRANE', assetCode });
  const site = await apiJSON(page, 'POST', '/api/v1/projects/P001/locations', csrf, { kind: 'SITE', name: code('LOC', 'R6-S'), code: code('LOC', 'R6-S-CODE') });
  expect(site.status).toBe(201);
  const ws1Name = code('LOC', 'R6-W1'), ws2Name = code('LOC', 'R6-W2');
  const ws1 = await apiJSON(page, 'POST', '/api/v1/projects/P001/locations', csrf, { kind: 'WORKSHOP', name: ws1Name, code: ws1Name, parentId: site.json!.id });
  const ws2 = await apiJSON(page, 'POST', '/api/v1/projects/P001/locations', csrf, { kind: 'WORKSHOP', name: ws2Name, code: ws2Name, parentId: site.json!.id });
  expect([ws1.status, ws2.status]).toEqual([201, 201]);

  await page.goto(`/#/assets/${a.id}`);
  await expect(section(page).getByTestId('asset-location-empty')).toContainText('该资产尚未安装到任何位置');

  const moveForm = page.getByTestId('asset-move-form');
  for (const [name, id] of [[ws1Name, ws1.json!.id], [ws2Name, ws2.json!.id]] as const) {
    await actions(page).getByRole('button', { name: '移动', exact: true }).click();
    await moveForm.getByLabel('目标位置').selectOption({ label: `${name}（车间）` });
    await moveForm.getByRole('button', { name: '移动', exact: true }).click();
    await expect(moveForm).toBeHidden();
    await expect(section(page).getByTestId('asset-location-current')).toHaveAttribute('data-location-id', String(id));
  }
  await expect(section(page).getByTestId('asset-location-path')).toContainText(`车间·${ws2Name}`);
  await expect(section(page).getByTestId('asset-location-path')).toContainText('场地·');
  await expect(section(page)).not.toContainText('经度');
  await page.reload();
  await expect(section(page).getByTestId('asset-location-current')).toHaveAttribute('data-location-id', String(ws2.json!.id));
  await page.screenshot({ path: 'test-results-r6/r6-current-location.png' });

  await section(page).getByTestId('asset-location-history-open').click();
  const rows = page.getByTestId('asset-location-history').getByTestId('asset-location-history-table').locator('tbody tr');
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toHaveAttribute('data-current', 'true');
  await expect(rows.nth(0)).toContainText(ws2Name);
  await expect(rows.nth(1)).toContainText(ws1Name);
  await page.screenshot({ path: 'test-results-r6/r6-location-history.png' });
  await page.getByTestId('asset-location-history-close').click();

  // Server truth matches the UI.
  const readback = await apiJSON(page, 'GET', `/api/v1/assets/${a.id}/location`, csrf);
  expect(readback.status).toBe(200);
  expect((readback.json!.current as { locationId: string }).locationId).toBe(ws2.json!.id);
  const hist = await apiJSON(page, 'GET', `/api/v1/assets/${a.id}/installation-history`, csrf);
  expect(hist.json!.total).toBe(2);

  await section(page).getByTestId('asset-location-link').click();
  await expect(page).toHaveURL(/#\/locations\?project=P001&location=/);
  await expect(page.getByTestId('location-table').locator(`tr[data-location-id="${ws2.json!.id}"]`)).toHaveAttribute('aria-current', 'location');

  // Equipment business loop on the initialized directory.
  await page.goto(`/#/assets/${a.id}`);
  await detail(page).locator('.tabs').getByRole('button', { name: '设备', exact: true }).click();
  await detail(page).getByRole('button', { name: '安装器件', exact: true }).click();
  const form = page.getByTestId('equipment-create-form');
  await expect(form.getByTestId('equipment-types-empty')).toHaveCount(0);
  await form.getByLabel('设备类型选择').selectOption('MOTOR');
  const eqCode = code('EQ', 'R6-MOTOR');
  await form.getByLabel('器件编号').fill(eqCode);
  await form.getByTestId('equipment-create-submit').click();
  await expect(form).toBeHidden();
  await expect(detail(page).locator('table.data-table')).toContainText(eqCode);
  await expect(detail(page).locator('table.data-table')).toContainText('电机（MOTOR）');
});

test('R6 跨项目：P002 用户读取 P001 资产位置/历史为 404；P001 用户读取 P002 固定资产位置为 404', async ({ browser }) => {
  const own = await (await browser.newContext()).newPage();
  await login(own, 'b4-admin');
  const csrf = await sessionCsrf(own);
  const a = await createAsset(own, csrf, { projectId: 'P001', assetType: 'CRANE', assetCode: code('CRN', 'R6-ISO') });
  for (const path of ['location', 'installation-history']) {
    expect((await apiJSON(own, 'GET', `/api/v1/assets/${a.id}/${path}`, csrf)).status).toBe(200);
    expect((await apiJSON(own, 'GET', `/api/v1/assets/b4-p002-asset/${path}`, csrf)).status).toBe(404);
  }
  await own.close();
  const other = await (await browser.newContext()).newPage();
  await login(other, 'b4-p002');
  const csrf2 = await sessionCsrf(other);
  for (const path of ['location', 'installation-history']) {
    const res = await apiJSON(other, 'GET', `/api/v1/assets/${a.id}/${path}`, csrf2);
    expect(res.status).toBe(404);
    expect(JSON.stringify(res.json)).not.toContain('P001');
    expect((await apiJSON(other, 'GET', `/api/v1/assets/b4-p002-asset/${path}`, csrf2)).status).toBe(200);
  }
  await other.goto(`/#/assets/${a.id}`);
  await expect(other.getByTestId('asset-load-error')).toBeVisible();
  await expect(other.getByTestId('asset-current-location')).toHaveCount(0);
  await other.close();
});
