// Real-browser (mocked API, no server writes) check that the command-bar project
// selector is visible, unobstructed and operable at common desktop widths, and
// that ledger rows are addressable by a stable asset id. Mirrors the B4
// Keycloak + PostgreSQL specs, which run only in CI.
import { test, expect, type Page } from '@playwright/test';
import { expectUnobstructed } from '../b4/run';

const session = {
  userId: 'u', displayName: 'admin',
  projects: [{ projectId: 'P001', projectName: 'Project-A', role: 'platform_admin' }, { projectId: 'P002', projectName: 'Project-B', role: 'viewer' }],
  csrfToken: 'x', expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z',
};
// Two assets whose code equals their display name (server fallback when no name).
const assets = [
  { id: 'A1', projectId: 'P001', displayName: 'CRN-1', assetCode: 'CRN-1', assetType: 'CRANE', assetStatus: 'DRAFT', manufacturerId: null, manufacturerSnapshot: '', brandSnapshot: '', modelSnapshot: '', serialNumber: null, manufactureDate: null, installationDate: null, commissioningDate: null, liftingHeightM: null, applicationType: null, controlMode: null, version: 1, specialEquipmentCode: null, useRegistrationNo: null },
];
async function mock(page: Page) {
  await page.route('**/auth/session', (r) => r.fulfill({ json: session }));
  await page.route('**/api/v1/**', (r) => {
    const path = new URL(r.request().url()).pathname;
    if (path === '/api/v1/assets') return r.fulfill({ json: assets });
    if (path === '/api/v1/assets/A1') return r.fulfill({ json: assets[0] });
    return r.fulfill({ json: [] });
  });
}

for (const [w, h] of [[1280, 720], [1366, 768], [1440, 900], [1920, 1080]] as const) {
  test(`顶栏项目选择 ${w}×${h}：可见未遮挡、可选择/清除、保持当前页`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: h });
    await mock(page);
    await page.goto('/#/assets');
    const scope = page.getByLabel('当前项目范围');
    await expectUnobstructed(page, scope, '当前项目范围');
    await scope.selectOption('P001');
    await expect(page).toHaveURL(/#\/assets\?project=P001$/);
    await expect(page.getByTestId('asset-center')).toBeVisible();
    await scope.selectOption('');
    await expect(page).toHaveURL(/#\/assets$/);
    await page.goto('/#/catalog');
    await expect(page.getByTestId('scope-fixed')).toContainText('全部已授权项目');
    await expect(page.getByLabel('当前项目范围')).toHaveCount(0);
    await page.goto('/#/overview');
    const focus = page.getByLabel('地图聚焦项目');
    await expectUnobstructed(page, focus, '地图聚焦项目');
    await focus.selectOption('P001');
    await expect(page).toHaveURL(/#\/overview\?project=P001$/);
    await focus.selectOption('');
    await expect(page).toHaveURL(/#\/overview$/);
    await expect(page.locator('.scene-mode-dock')).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0);
  });
}

test('台账行按稳定资产 ID 定位：编号与名称相同时仍唯一', async ({ page }) => {
  await mock(page);
  await page.goto('/#/assets?project=P001');
  await expect(page.getByTestId('asset-table').getByText('CRN-1')).toHaveCount(2); // the ambiguity B4 hit
  const row = page.locator('tr[data-asset-id="A1"]');
  await expect(row).toHaveCount(1);
  await expect(row.locator('td[data-col="assetCode"]')).toHaveText('CRN-1');
  await row.click();
  await expect(page.getByTestId('asset-detail')).toHaveAttribute('data-asset-id', 'A1');
  await expect(page.getByTestId('asset-detail')).toHaveAttribute('data-project-id', 'P001');
});
