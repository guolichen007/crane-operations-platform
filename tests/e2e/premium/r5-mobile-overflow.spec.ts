// R5-UX-PERF B: mobile horizontal overflow. Real browser, mocked API (no server writes).
// Every main page at 375/390/768/1366/1920: document scrollWidth must equal the viewport
// width, and no visible element may extend past the right edge — measured, not hidden:
// the test also fails if html/body/#app/.shell hide horizontal overflow.
// Baseline 973e134 (CC report): 375 px → scrollWidth 462 px.
import { test, expect, type Page } from '@playwright/test';
import { mkdirSync, writeFileSync } from 'node:fs';
import { inventorySummary, routeSummary } from '../summary-mock';

const session = {
  userId: 'mobile',
  displayName: '移动端验收',
  projects: [
    { projectId: 'P001', projectName: 'Project-A 海宁一期', role: 'platform_admin' },
    { projectId: 'P002', projectName: 'Project-B', role: 'viewer' },
  ],
  csrfToken: 'x'.repeat(32),
  expiresAt: '2099-01-01T00:00:00Z',
  idleExpiresAt: '2099-01-01T00:00:00Z',
};
const projects = [
  { id: 'P001', name: 'Project-A 海宁一期', lifecycle: 'PLANNED', province: '浙江省', city: '嘉兴市', county: '海宁市', locationLabel: '浙江省 嘉兴市 海宁市', adminCode: 330481, scheduleText: '2026-11', notes: '', simulatedCount: 1, runtimeDeviceCount: 1, realConnectedCount: 0, assetDeviceCount: 2 },
  { id: 'P002', name: 'Project-B', lifecycle: 'PLANNED', province: '', city: '', county: '', locationLabel: '', adminCode: null, scheduleText: '', notes: '', simulatedCount: 0, runtimeDeviceCount: 0, realConnectedCount: 0, assetDeviceCount: 1 },
];
const inventoryAssets = [
  { assetDeviceId: 'P001-A01', projectId: 'P001', displayName: '1 号桥式起重机（双梁）', craneModel: 'QD 32/5t-22.5m A5', ratedCapacityT: 32, spanM: 22.5, workClass: 'A5', grabType: null, lifecycle: 'PLANNED', runtimeSource: 'SIMULATION', runtimeDeviceId: 'SIM-P001-01', connectivity: 'MQTT_CONNECTED' },
  { assetDeviceId: 'P001-A02', projectId: 'P001', displayName: '1 号桥式起重机（双梁）', craneModel: null, ratedCapacityT: null, spanM: null, workClass: null, grabType: null, lifecycle: 'PLANNED', runtimeSource: null, runtimeDeviceId: null, connectivity: null },
  { assetDeviceId: 'P002-A01', projectId: 'P002', displayName: 'B 线行车', craneModel: null, ratedCapacityT: null, spanM: null, workClass: null, grabType: null, lifecycle: 'PLANNED', runtimeSource: null, runtimeDeviceId: null, connectivity: null },
];
const ledgerAsset = (id: string, code: string | null) => ({ id, projectId: 'P001', displayName: '1 号桥式起重机（双梁）', assetCode: code, assetType: 'CRANE', assetStatus: 'ACTIVE', manufacturerId: null, manufacturerSnapshot: '', brandSnapshot: '', modelSnapshot: 'QD 32/5t-22.5m', serialNumber: null, manufactureDate: null, installationDate: null, commissioningDate: null, liftingHeightM: null, applicationType: null, controlMode: null, version: 1, specialEquipmentCode: null, useRegistrationNo: null, currentLocationId: null });

async function mock(page: Page) {
  await page.route('**/auth/session', (r) => r.fulfill({ json: session }));
  await page.route('**/api/v1/**', (r) => {
    const p = new URL(r.request().url()).pathname.replace('/api/v1/', '');
    if (p === 'projects') return r.fulfill({ json: projects });
    if (p === 'assets/devices') return r.fulfill({ json: inventoryAssets });
    if (p === 'assets') return r.fulfill({ json: [ledgerAsset('A1', 'HN-QD-0001'), ledgerAsset('A2', null)] });
    if (/^assets\/A\d$/.test(p)) return r.fulfill({ json: ledgerAsset(p.split('/')[1]!, p.endsWith('1') ? 'HN-QD-0001' : null) });
    if (p === 'projects/P001/locations') return r.fulfill({ json: [{ id: 'L1', projectId: 'P001', parentId: null, kind: 'SITE', code: 'S1', name: '海宁一号场地（东区主厂房）', province: '浙江省', city: '嘉兴市', county: '海宁市', longitude: null, latitude: null, coordinateSystem: 'UNKNOWN', coordinateSource: 'MANUAL', status: 'ACTIVE', version: 1, address: '浙江省嘉兴市海宁市某工业园区 18 号' }] });
    if (p === 'manufacturers') return r.fulfill({ json: [{ id: 'M1', name: '某起重机械股份有限公司', country: 'CN', status: 'ACTIVE', version: 1 }] });
    return r.fulfill({ json: [] });
  });
  await routeSummary(page, async (c) =>
    inventorySummary(
      projects.map((p) => ({ id: p.id, adminCode: p.adminCode })),
      inventoryAssets.map((a) => ({ projectId: a.projectId })),
      c.region
    )
  );
}

const PAGES = ['/overview', '/overview?view=2d', '/portfolio', '/projects', '/devices', '/assets', '/assets/A2', '/locations?project=P001', '/catalog', '/runtime', '/alarms'];
const SIZES = [375, 390, 768, 1366, 1920];

async function overflow(page: Page) {
  return page.evaluate(() => {
    const vw = document.documentElement.clientWidth;
    const offenders: { sel: string; right: number; width: number }[] = [];
    for (const el of document.querySelectorAll<HTMLElement>('body *')) {
      const r = el.getBoundingClientRect();
      if (!r.width || !r.height || r.right <= vw + 0.5) continue;
      // Inside a horizontally scrollable/clipping container that itself fits: not a page overflow.
      let clipped = false;
      for (let a = el.parentElement; a && a !== document.body; a = a.parentElement) {
        const s = getComputedStyle(a);
        if (/(auto|scroll|hidden|clip)/.test(s.overflowX) && a.getBoundingClientRect().right <= vw + 0.5) {
          clipped = true;
          break;
        }
      }
      if (clipped || getComputedStyle(el).visibility === 'hidden') continue;
      const sel = el.tagName.toLowerCase() + (el.id ? '#' + el.id : '') + (el.classList.length ? '.' + [...el.classList].slice(0, 3).join('.') : '') + (el.dataset.testid ? `[data-testid=${el.dataset.testid}]` : '');
      offenders.push({ sel, right: Math.round(r.right), width: Math.round(r.width) });
    }
    // Keep only outermost offenders (a child of an offender is not reported again).
    const hidden = ['html', 'body', '#app', '.shell'].map((s) => {
      const e = document.querySelector(s);
      return e ? `${s}:${getComputedStyle(e).overflowX}` : '';
    });
    // The navigation rail must not paint over the page content (it did at 375 px).
    const rail = document.querySelector('.side-rail')?.getBoundingClientRect();
    const app = document.querySelector('.application, .shell-body > main, main')?.getBoundingClientRect();
    const railOverlap = rail && app ? Math.max(0, Math.round(rail.right - app.left)) : 0;
    const railLabels = [...document.querySelectorAll<HTMLElement>('.side-rail nav span')].filter((e) => e.getBoundingClientRect().width > 0 && getComputedStyle(e).display !== 'none').length;
    // Stage HUD controls must not overlap each other.
    const hud = [...document.querySelectorAll<HTMLElement>('[data-testid=spatial-stage] .map-view-switch, [data-testid=spatial-stage] .observation-switch, [data-testid=spatial-stage] .scene-mode-dock, [data-testid=spatial-stage] .map-tools, [data-testid=spatial-stage] .map-topline > *')]
      .map((e) => ({ c: e.className, r: e.getBoundingClientRect() }))
      .filter((x) => x.r.width && x.r.height);
    const hudOverlaps: string[] = [];
    for (let i = 0; i < hud.length; i++)
      for (let j = i + 1; j < hud.length; j++) {
        const a = hud[i]!.r, b = hud[j]!.r;
        if (a.left < b.right - 1 && b.left < a.right - 1 && a.top < b.bottom - 1 && b.top < a.bottom - 1) hudOverlaps.push(`${hud[i]!.c} × ${hud[j]!.c}`);
      }
    return { vw, scrollWidth: document.documentElement.scrollWidth, offenders: offenders.slice(0, 12), hidden, railOverlap, railLabels, hudOverlaps };
  });
}

for (const w of SIZES)
  test(`移动/桌面 ${w}px：主要页面无全局横向溢出`, async ({ page }) => {
    test.setTimeout(150000);
    const h = w < 800 ? 812 : w === 1366 ? 768 : 1080;
    await page.setViewportSize({ width: w, height: h });
    await mock(page);
    const rows: unknown[] = [];
    const dir = process.env.R5_MOBILE_OUT ? `${process.env.R5_MOBILE_OUT}` : test.info().outputPath('mobile');
    mkdirSync(dir, { recursive: true });
    for (const path of PAGES) {
      await page.goto('/#' + path);
      await page.waitForLoadState('networkidle');
      await page.waitForTimeout(path.startsWith('/overview') && !path.includes('2d') ? 2500 : 600);
      const m = await overflow(page);
      rows.push({ path, ...m });
      await page.screenshot({ path: `${dir}/${w}-${path.replace(/[/?=&]+/g, '_')}.png`, fullPage: false });
    }
    writeFileSync(`${dir}/metrics-${w}.json`, JSON.stringify(rows, null, 1));
    await test.info().attach(`metrics-${w}`, { body: JSON.stringify(rows, null, 1), contentType: 'application/json' });
    console.log('MOBILE', w, JSON.stringify(rows.map((r) => { const x = r as { path: string; scrollWidth: number; offenders: { sel: string; right: number }[]; hidden: string[] }; return [x.path, x.scrollWidth, x.offenders.slice(0, 4).map((o) => `${o.sel}@${o.right}`), x.hidden.filter((s) => /hidden|clip/.test(s))]; })));
    for (const r of rows as { path: string; vw: number; scrollWidth: number; offenders: unknown[]; hidden: string[]; railOverlap: number; railLabels: number }[]) {
      expect(r.railOverlap, `${r.path}: side rail overlaps content`).toBeLessThanOrEqual(1);
      expect((r as unknown as { hudOverlaps: string[] }).hudOverlaps, `${r.path}: map controls overlap`).toEqual([]);
      if (w <= 700) expect(r.railLabels, `${r.path}: phone rail is icon-only`).toBe(0);
      expect(r.hidden.filter((s) => /:(hidden|clip)$/.test(s) && !s.startsWith('.shell')), `${r.path}: overflow must not be hidden globally`).toEqual([]);
      expect(r.scrollWidth, `${r.path} @${w}: scrollWidth ${r.scrollWidth} > ${r.vw}; ${JSON.stringify(r.offenders)}`).toBeLessThanOrEqual(r.vw);
    }
  });

// R5-UX-PERF D: wording and identification (same mocked inventory).
test('D 运行身份入口为「查看运行状态」，不出现「最近状态 / LIVE」；空编号显示待补全与内部 ID', async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 900 });
  await mock(page);
  await page.goto('/#/devices');
  const rows = page.getByTestId('inventory-asset');
  await expect(rows).toHaveCount(3);
  await expect(page.getByText('最近状态 / LIVE')).toHaveCount(0);
  const link = rows.first().getByRole('link', { name: /查看运行状态/ });
  await expect(link).toHaveAttribute('href', '#/devices/SIM-P001-01');
  // Assets without a runtime identity offer no runtime entry at all.
  await expect(rows.nth(1).getByRole('link', { name: /查看运行状态/ })).toHaveCount(0);
  // Same-name assets stay distinguishable by internal asset id + project.
  await expect(rows.nth(0)).toContainText('P001-A01');
  await expect(rows.nth(1)).toContainText('P001-A02');
  await page.goto('/#/assets');
  const missing = page.locator('tr[data-asset-id=A2] [data-testid=asset-code-missing]');
  await expect(missing).toContainText('编号待补全');
  await expect(missing).toContainText('内部 ID A2');
  await expect(page.locator('tr[data-asset-id=A1] td[data-col=assetCode]')).toHaveText('HN-QD-0001');
  await page.goto('/#/assets/A2');
  await expect(page.getByTestId('asset-detail').getByTestId('asset-code-missing')).toContainText('项目 P001');
});
