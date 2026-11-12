// Q4-QA-R1 §3: Equipment removal pins the version shown when the confirmation opened.
// Real browser against a stateful mocked API that enforces If-Match like the server (409 on a stale
// version, the record untouched). The real Keycloak + PostgreSQL proof is tests/e2e/b4/asset-crud.spec.ts.
import { test, expect, type Page } from '@playwright/test';

const session = { userId: 'u', displayName: 'admin', projects: [{ projectId: 'P001', projectName: 'Project-A', role: 'platform_admin' }], csrfToken: 'x'.repeat(32), expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' };
const asset = { id: 'A1', projectId: 'P001', displayName: '1 号行车', assetCode: 'C1', assetType: 'CRANE', assetStatus: 'ACTIVE', manufacturerId: null, manufacturerSnapshot: '', brandSnapshot: '', modelSnapshot: '', serialNumber: null, manufactureDate: null, installationDate: null, commissioningDate: null, liftingHeightM: null, applicationType: null, controlMode: null, version: 1, specialEquipmentCode: null, useRegistrationNo: null };

async function mock(page: Page, removeDelayMs = 0) {
  const eq = { id: 'EQ-1', assetId: 'A1', equipmentCode: 'MOTOR-01', typeCode: 'MOTOR', catalogItemId: null, serialNumber: null, semanticSlot: null, positionLabel: null, criticality: 'MEDIUM', administrativeState: 'IN_SERVICE', version: 3, removedAt: null as string | null };
  const state = { eq, removes: [] as (string | null)[] };
  await page.route('**/auth/session', (r) => r.fulfill({ json: session }));
  await page.route('**/api/v1/**', async (r) => {
    const u = new URL(r.request().url());
    const m = r.request().method();
    if (u.pathname === '/api/v1/assets/A1') return r.fulfill({ json: asset });
    if (u.pathname === '/api/v1/assets/A1/equipment') return r.fulfill({ json: [state.eq] });
    if (u.pathname === '/api/v1/equipment/types') return r.fulfill({ json: [{ typeCode: 'MOTOR', displayName: '电机', domain: 'MECHANICAL', specSchemaVersion: '1', status: 'ACTIVE' }] });
    if (u.pathname === '/api/v1/equipment/EQ-1/remove' && m === 'POST') {
      const ifMatch = r.request().headers()['if-match'] ?? null;
      state.removes.push(ifMatch);
      if (removeDelayMs) await new Promise((x) => setTimeout(x, removeDelayMs));
      if (ifMatch !== `"${state.eq.version}"`) return r.fulfill({ status: 409, json: { error: 'version conflict' } });
      state.eq = { ...state.eq, removedAt: '2026-11-12T00:00:00Z', version: state.eq.version + 1 };
      return r.fulfill({ json: { status: 'REMOVED' } });
    }
    return r.fulfill({ json: [] });
  });
  return state;
}

/**
 * A real background refetch, as the app does it in production: once the cached Equipment list is
 * stale (staleTime 30 s), a network reconnect makes vue-query refetch the mounted queries.
 */
async function backgroundRefetch(page: Page) {
  await page.waitForTimeout(31_000);
  await page.context().setOffline(true);
  await page.context().setOffline(false);
}

async function openRemove(page: Page) {
  await page.goto('/#/assets/A1');
  await page.getByRole('button', { name: '设备', exact: true }).click();
  await expect(page.locator('tr.data-row')).toContainText('v3');
  await page.getByRole('button', { name: '拆卸', exact: true }).click();
  return page.getByTestId('equipment-remove-dialog');
}

test.setTimeout(120_000);
test('拆卸确认框固定打开时的版本：他人修改后列表刷新，确认拆卸仍按旧版本提交 → 409，记录未被拆卸', async ({ page }) => {
  const state = await mock(page);
  const dialog = await openRemove(page);
  await expect(dialog).toContainText('MOTOR-01');
  await expect(dialog).toContainText('v3');
  // Another session edits the Equipment (v3 → v4); this page's list refreshes in the background.
  state.eq = { ...state.eq, criticality: 'HIGH', version: 4 };
  await backgroundRefetch(page);
  await expect(page.locator('tr.data-row')).toContainText('v4');
  await dialog.getByRole('button', { name: '确认拆卸' }).click();
  await expect(dialog.getByRole('alert')).toContainText('已被他人修改');
  expect(state.removes).toEqual(['"3"']);
  expect(state.eq.removedAt).toBeNull();
  await expect(page.getByText('拆卸成功')).toHaveCount(0);
  await page.screenshot({ path: 'test-results/q4-equipment/remove-409.png' });
  // Closing and reopening fetches the current version; then the removal succeeds.
  await dialog.getByRole('button', { name: '取消' }).click();
  await page.getByRole('button', { name: '拆卸', exact: true }).click();
  await expect(dialog).toContainText('v4');
  await dialog.getByRole('button', { name: '确认拆卸' }).click();
  await expect(dialog).toBeHidden();
  expect(state.removes).toEqual(['"3"', '"4"']);
  await expect(page.locator('tr.data-row')).toContainText('已拆卸');
});

test('拆卸提交中：按钮禁用、Esc/蒙层/取消均不能关闭，双击只发一次请求', async ({ page }) => {
  const state = await mock(page, 1500);
  const dialog = await openRemove(page);
  const confirm = dialog.getByRole('button', { name: '确认拆卸' });
  await confirm.dblclick();
  await expect(dialog.getByRole('button', { name: '拆卸中…' })).toBeDisabled();
  await expect(dialog.getByRole('button', { name: '取消' })).toBeDisabled();
  await page.keyboard.press('Escape');
  await page.locator('.modal-backdrop').click({ position: { x: 5, y: 5 } });
  await expect(dialog).toBeVisible();
  await expect(dialog).toBeHidden();
  expect(state.removes).toEqual(['"3"']);
});
