// Q4-QA-R1 §2: the login identity / project grants change INSIDE one SPA lifetime (no page reload):
// another tab logged out and in as a different account, or a ProjectGrant was revoked, and this tab
// re-reads /auth/session (idle-expiry check, auth retry). Nothing of the previous identity — map
// projects, homepage project list, global search, asset ledger — may stay visible, and a late
// response of the previous identity must not overwrite the new identity's data.
// Real browser against a stateful mocked BFF (no server writes).
import { test, expect, type Page } from '@playwright/test';
import { inventorySummary, routeSummary } from '../summary-mock';

type Grant = { projectId: string; projectName: string; role: string };
type Who = { user: string; grants: Grant[] };
const A: Who = { user: 'user-a', grants: [{ projectId: 'PA', projectName: 'Alpha 项目', role: 'viewer' }] };
const B: Who = { user: 'user-b', grants: [{ projectId: 'PB', projectName: 'Beta 项目', role: 'viewer' }] };
const NONE: Who = { user: 'user-none', grants: [] };
const CODE: Record<string, number> = { PA: 330481, PB: 440300 };
const NAMES: Record<string, [string, string]> = { PA: ['浙江省', '嘉兴市'], PB: ['广东省', '深圳市'] };
const LABEL: Record<string, string> = { PA: 'Alpha', PB: 'Beta' };

const invProject = (id: string) => ({ id, name: `${LABEL[id]} 项目`, lifecycle: 'PLANNED', province: NAMES[id][0], city: NAMES[id][1], county: '', locationLabel: NAMES[id].join(' '), adminCode: CODE[id], scheduleText: '', notes: '', assetDeviceCount: 1, runtimeDeviceCount: 0, realConnectedCount: 0, simulatedCount: 0 });
const invAsset = (id: string) => ({ assetDeviceId: `${LABEL[id]}-行车-1`, projectId: id, displayName: `${LABEL[id]}-行车-1`, craneModel: null, ratedCapacityT: null, spanM: null, workClass: null, grabType: null, lifecycle: 'PLANNED', runtimeSource: null, runtimeDeviceId: null, connectivity: null });
const master = (id: string) => ({ id: `AM-${id}`, projectId: id, displayName: `${LABEL[id]}资产台账`, assetCode: `${id}-001`, assetType: 'CRANE', assetStatus: 'ACTIVE', manufacturerId: null, manufacturerSnapshot: '', brandSnapshot: '', modelSnapshot: '', serialNumber: null, manufactureDate: null, installationDate: null, commissioningDate: null, liftingHeightM: null, applicationType: null, controlMode: null, version: 1, specialEquipmentCode: null, useRegistrationNo: null });

/**
 * `who` is what the BFF currently believes the cookie belongs to. Each response is computed for the
 * identity AT REQUEST TIME (like a real BFF), and may be delayed so it arrives after a switch.
 */
async function mock(page: Page, first: Who, delays: { assetsFor?: string; projectsFor?: string; ms?: number } = {}) {
  const state = { who: first, idleSoon: true, routed: [] as string[] };
  await page.route('**/auth/session', (r) => {
    // The first identity's idle deadline is a few seconds away: the AuthGate then re-reads the
    // session in place — exactly the path on which another tab's login becomes visible here.
    const idle = state.idleSoon ? new Date(Date.now() + 2500).toISOString() : '2099-01-01T00:00:00Z';
    return r.fulfill({ json: { userId: state.who.user, displayName: state.who.user, projects: state.who.grants, csrfToken: 'x'.repeat(32), expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: idle } });
  });
  await page.route('**/api/v1/**', async (r) => {
    const who = state.who;
    const ids = who.grants.map((g) => g.projectId);
    const p = new URL(r.request().url()).pathname.replace('/api/v1/', '');
    state.routed.push(`${who.user}:${p}`);
    if (p === 'assets' && delays.assetsFor === who.user) await new Promise((x) => setTimeout(x, delays.ms ?? 3000));
    if (p === 'projects' && delays.projectsFor === who.user) await new Promise((x) => setTimeout(x, delays.ms ?? 3000));
    if (p === 'projects') return r.fulfill({ json: ids.map(invProject) });
    if (p === 'assets/devices') return r.fulfill({ json: ids.map(invAsset) });
    if (p === 'assets') return r.fulfill({ json: ids.map(master) }).catch(() => undefined);
    return r.fulfill({ json: [] });
  });
  await routeSummary(page, (c) => inventorySummary(state.who.grants.map((g) => ({ id: g.projectId, adminCode: CODE[g.projectId] })), state.who.grants.map((g) => ({ projectId: g.projectId })), c.region));
  return state;
}

async function search(page: Page, text: string) {
  await page.keyboard.press('Control+K');
  const box = page.getByRole('dialog', { name: '全局搜索' });
  await box.getByRole('textbox').fill(text);
  return box;
}
async function closeSearch(page: Page) { await page.keyboard.press('Escape'); }
/** Switch identity server-side, then wait until THIS tab has re-read the session in place. */
async function becomes(page: Page, state: { who: Who; idleSoon: boolean }, next: Who) {
  state.who = next;
  state.idleSoon = false;
  await expect(page.getByTestId('session-user').first()).toHaveText(next.user, { timeout: 15000 });
}

async function loadAsA(page: Page) {
  await page.goto('/#/overview?view=2d');
  await expect(page.locator('.plan-item[data-project-id="PA"]')).toBeVisible();
  const s = await search(page, 'Alpha');
  await expect(s.locator('[data-result-kind]').first()).toBeVisible();
  await closeSearch(page);
  await page.evaluate(() => (location.hash = '#/assets'));
  await expect(page.getByTestId('asset-table')).toContainText('Alpha资产台账');
  await page.evaluate(() => (location.hash = '#/overview?view=2d'));
  await expect(page.locator('.plan-item[data-project-id="PA"]')).toBeVisible();
}

for (const [name, next, own] of [
  ['另一账号 B（仅有其他项目权限）', B, 'Beta'],
  ['无任何授权账号', NONE, null],
] as const) {
  test(`同一 SPA 内身份 A → ${name}：地图、项目列表、全局搜索、资产台账不残留 A 的数据`, async ({ page }) => {
    const state = await mock(page, A);
    await loadAsA(page);
    await becomes(page, state, next);
    await page.screenshot({ path: `test-results/q4-identity/${next.user}-overview.png` });

    // Homepage project list / map data.
    await expect(page.locator('.plan-item[data-project-id="PA"]')).toHaveCount(0);
    if (own) await expect(page.locator('.plan-item[data-project-id="PB"]')).toBeVisible();
    // Global search.
    const s = await search(page, 'Alpha');
    await expect(s.locator('[data-result-kind]')).toHaveCount(0);
    await closeSearch(page);
    if (own) {
      const s2 = await search(page, own);
      await expect(s2.locator('[data-result-kind]').first()).toBeVisible();
      await closeSearch(page);
    }
    // Asset ledger (vue-query cache).
    await page.evaluate(() => (location.hash = '#/assets'));
    await expect(page.getByTestId('asset-center')).toBeVisible();
    await expect(page.getByTestId('asset-center')).not.toContainText('Alpha资产台账');
    if (own) await expect(page.getByTestId('asset-table')).toContainText('Beta资产台账');
    await page.screenshot({ path: `test-results/q4-identity/${next.user}-assets.png` });
  });
}

test('授权撤销（同一账号 ProjectGrant 减少）：被撤销项目从地图、搜索、台账消失', async ({ page }) => {
  const both: Who = { user: 'user-a', grants: [...A.grants, ...B.grants] };
  const state = await mock(page, both);
  await page.goto('/#/overview?view=2d');
  await expect(page.locator('.plan-item[data-project-id="PA"]')).toBeVisible();
  await page.evaluate(() => (location.hash = '#/assets'));
  await expect(page.getByTestId('asset-table')).toContainText('Alpha资产台账');
  state.who = { user: 'user-a', grants: B.grants };
  state.idleSoon = false;
  await expect(page.getByTestId('session-projects').first()).not.toContainText('Alpha', { timeout: 15000 });
  await page.screenshot({ path: 'test-results/q4-identity/revoked-assets.png' });
  await expect(page.getByTestId('asset-center')).not.toContainText('Alpha资产台账');
  await expect(page.getByTestId('asset-table')).toContainText('Beta资产台账');
  await page.evaluate(() => (location.hash = '#/overview?view=2d'));
  await expect(page.locator('.plan-item[data-project-id="PB"]')).toBeVisible();
  await expect(page.locator('.plan-item[data-project-id="PA"]')).toHaveCount(0);
  const s = await search(page, 'Alpha');
  await expect(s.locator('[data-result-kind]')).toHaveCount(0);
});

test('乱序：A 的台账请求在切换后才返回，不覆盖 B 的数据', async ({ page }) => {
  const state = await mock(page, A, { assetsFor: 'user-a', ms: 4000 });
  await page.goto('/#/assets');
  await expect(page.getByTestId('asset-loading')).toBeVisible();
  await expect.poll(() => state.routed.includes('user-a:assets')).toBe(true);
  // A's ledger request is in flight (4 s) when the identity becomes B.
  await becomes(page, state, B);
  await page.waitForTimeout(4500); // A's late response arrives now
  await page.screenshot({ path: 'test-results/q4-identity/out-of-order-assets.png' });
  await expect(page.getByTestId('asset-table')).toContainText('Beta资产台账');
  await expect(page.getByTestId('asset-center')).not.toContainText('Alpha资产台账');
});

test('乱序：A 的地图数据（Inventory 项目）在切换后才返回，地图与搜索只显示 B', async ({ page }) => {
  const state = await mock(page, A, { projectsFor: 'user-a', ms: 4000 });
  await page.goto('/#/overview?view=2d');
  // A's Inventory request has reached the BFF as A and is now held for 4 s.
  await expect.poll(() => state.routed.includes('user-a:projects')).toBe(true);
  await becomes(page, state, B);
  await expect(page.locator('.plan-item[data-project-id="PB"]')).toBeVisible();
  await page.waitForTimeout(4500); // A's late Inventory response arrives now
  await page.screenshot({ path: 'test-results/q4-identity/out-of-order-map.png' });
  await expect(page.locator('.plan-item[data-project-id="PA"]')).toHaveCount(0);
  await expect(page.locator('.plan-item[data-project-id="PB"]')).toBeVisible();
  const s = await search(page, 'Alpha');
  await expect(s.locator('[data-result-kind]')).toHaveCount(0);
});
