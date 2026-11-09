// H1-R1 homepage acceptance closure (real browser, mocked API — no server writes).
// Each test targets one review finding and asserts the observable data semantics,
// not merely that an error banner exists:
//   R1-1 API failure never shows a fabricated 0 (loading / unavailable / empty / stale / recovery)
//   R1-2 one statistics scope for KPI, Attention, Inspector (source ∩ region; status only filters display)
//   R1-3 fetch time is labelled as a client fetch, not telemetry "as of"
//   R1-4 selection stays visible next to the drawer; Esc closes detail, then goes up one level (2D = 3D)
//   R1-5 WebGL context loss / map resource failure are recoverable, never a blank stage
//   R1-6 usable map area with the Inspector open
// Counter-proofs (revert the fix → the test fails) are recorded in
// docs/04-前端与可视化/Homepage-H1-R1-验收报告.md.
import { test, expect, type Page, type Route } from '@playwright/test';
import { readFileSync, writeFileSync } from 'node:fs';
import { cloudDevice, cloudAlarm } from '../../../apps/web-console/tests/cloud-fixtures';
import { inventorySummary, routeSummary, type SummaryCall, type SummaryReply } from '../summary-mock';
import type { SummaryOverrides } from '../../../apps/web-console/tests/summary-fixtures';

type Proj = { id: string; name: string; adminCode: number | null; devices: number };
type Asset = { id: string; projectId: string; alarm?: boolean };

function session(projects: Proj[]) {
  return {
    userId: 'r1',
    displayName: 'R1 审查',
    projects: projects.map((p) => ({ projectId: p.id, projectName: p.name, role: 'viewer' })),
    csrfToken: 'synthetic-csrf-test-only',
    expiresAt: '2099-01-01T00:00:00Z',
    idleExpiresAt: '2099-01-01T00:00:00Z',
  };
}

/**
 * Programmable Inventory API. `projectsResponse` decides per call what
 * /api/v1/projects returns, so a test can fail the first load, the second, etc.
 */
async function api(
  page: Page,
  projects: Proj[],
  assets: Asset[],
  opts: {
    projectsResponse?: (n: number) => 'ok' | 'fail' | 'hang';
    holdMs?: number;
    /** H2-1D: the homepage numbers come from the operations summary; default = derived from this inventory. */
    summary?: (c: SummaryCall) => SummaryReply | 'ok';
    metrics?: SummaryOverrides;
  } = {}
) {
  const state = { projectCalls: 0, summaryCalls: [] as SummaryCall[] };
  await page.route('**/auth/session', (r) => r.fulfill({ json: session(projects) }));
  const rawProjects = () =>
    projects.map((p) => ({
      id: p.id,
      name: p.name,
      lifecycle: 'PLANNED',
      province: '',
      city: '',
      county: '',
      locationLabel: '',
      adminCode: p.adminCode,
      scheduleText: '',
      notes: '',
      simulatedCount: 0,
      runtimeDeviceCount: 0,
      realConnectedCount: 0,
      assetDeviceCount: p.devices,
    }));
  const rawAssets = () =>
    assets.map((a) => ({
      assetDeviceId: a.id,
      projectId: a.projectId,
      displayName: a.id,
      craneModel: null,
      ratedCapacityT: null,
      spanM: null,
      workClass: null,
      grabType: null,
      lifecycle: 'PLANNED',
      runtimeSource: a.alarm ? 'SIMULATION' : null,
      runtimeDeviceId: a.alarm ? `RT-${a.id}` : null,
      connectivity: a.alarm ? 'MQTT_CONNECTED' : null,
    }));
  const shadows = () =>
    assets
      .filter((a) => a.alarm)
      .map((a) => {
        const s = cloudDevice(`RT-${a.id}`);
        s.reported.alarmState = 'ACTIVE';
        return s;
      });
  await page.route('**/api/v1/**', async (r: Route) => {
    const path = new URL(r.request().url()).pathname;
    if (path === '/api/v1/projects') {
      const n = ++state.projectCalls;
      const mode = opts.projectsResponse?.(n) ?? 'ok';
      if (opts.holdMs) await new Promise((res) => setTimeout(res, opts.holdMs));
      if (mode === 'hang') await new Promise((res) => setTimeout(res, 4000));
      if (mode === 'fail') return r.fulfill({ status: 503, json: { error: 'inventory unavailable' } });
      return r.fulfill({ json: rawProjects() });
    }
    return r.fulfill({
      json:
        path === '/api/v1/assets/devices'
          ? rawAssets()
          : path === '/api/v1/devices'
            ? shadows()
            : path === '/api/v1/alarms'
              ? shadows().length
                ? [cloudAlarm()]
                : []
              : {},
    });
  });
  state.summaryCalls = await routeSummary(page, async (c) => {
    if (opts.holdMs) await new Promise((res) => setTimeout(res, opts.holdMs));
    const out = opts.summary?.(c) ?? 'ok';
    return out === 'ok' ? inventorySummary(projects, assets, c.region, opts.metrics) : out;
  });
  return state;
}

const main = (page: Page) => page.locator('main.overview');
const kpi = (page: Page, id: string) => page.locator(`[data-kpi=${id}] .home-kpi-value`);
const attention = (page: Page, id: string) => page.locator(`[data-attention=${id}] b`);
const asof = (page: Page) => page.getByTestId('home-asof');

/** Example inventory: one located project (Haining 330481) with an alarm device, others unlocated. */
const located: Proj[] = [
  { id: 'P001', name: 'Project-A', adminCode: 330481, devices: 2 },
  { id: 'P002', name: 'Project-B', adminCode: null, devices: 1 },
];
const locatedAssets: Asset[] = [
  { id: 'A-1', projectId: 'P001', alarm: true },
  { id: 'A-2', projectId: 'P001' },
  { id: 'B-1', projectId: 'P002' },
];

// ---------------------------------------------------------------- R1-1
// H2-1D: the numbers are the operations summary; its failures (not the map's) decide the KPI state.
const KPI_IDS = ['registered', 'real-connected', 'alarm-real', 'pending-review', 'no-runtime'];
const ATTENTION_IDS = ['pending-review-alarm', 'real-offline', 'real-stale', 'real-unknown-time', 'unlocated'];
test.describe('R1-1 API 故障不显示假 0', () => {
  test('首次加载：LOADING 占位 → 503 不可用（KPI/Attention 均无数字）→ 重试成功切回正常', async ({ page }) => {
    await api(page, located, locatedAssets, { summary: (c) => (c.n === 1 ? 'fail' : 'ok'), holdMs: 1200 });
    await page.goto('/#/overview?view=2d');
    // LOADING: placeholders, never a number.
    await expect(page.getByTestId('home-kpis')).toHaveAttribute('data-state', 'loading');
    await expect(kpi(page, 'registered')).toHaveText('—');
    await expect(attention(page, 'real-offline')).toHaveText('—');
    await expect(asof(page)).toHaveAttribute('data-state', 'loading');
    // First failure: "不可用", with no digit anywhere in KPI values or Attention counts.
    await expect(page.getByTestId('home-kpis')).toHaveAttribute('data-state', 'unavailable');
    for (const id of KPI_IDS) {
      await expect(kpi(page, id)).toHaveText('不可用');
      await expect(kpi(page, id)).not.toHaveText(/\d/);
    }
    for (const id of ATTENTION_IDS) await expect(attention(page, id)).toHaveText('不可用');
    await expect(page.getByTestId('home-load-error')).toContainText('暂无可用数据');
    await expect(page.getByTestId('home-load-error')).toHaveAttribute('data-status', '503');
    await expect(asof(page)).toContainText('不可用');
    // Empty-state copy must not claim "no projects" when we simply could not load.
    await expect(page.getByTestId('home-empty')).toHaveCount(0);
    // Retry → ready, real numbers.
    await page.getByTestId('home-load-error').getByRole('button', { name: '重试' }).click();
    await expect(page.getByTestId('home-kpis')).toHaveAttribute('data-state', 'ready');
    await expect(kpi(page, 'registered')).toHaveText('3台');
    await expect(attention(page, 'unlocated')).toHaveText('1');
    // A legitimate 0 after a complete successful load (no REAL identity is connected).
    await expect(kpi(page, 'real-connected')).toHaveText('0台');
    await expect(page.getByTestId('home-load-error')).toHaveCount(0);
  });

  test('成功拉取后合法计数可以为 0（无授权项目全部为 0），未知/失败才不可用', async ({ page }) => {
    await api(page, [], []);
    await page.goto('/#/overview?view=2d');
    await expect(page.getByTestId('home-kpis')).toHaveAttribute('data-state', 'ready');
    await expect(kpi(page, 'registered')).toHaveText('0台');
    await expect(attention(page, 'real-offline')).toHaveText('0');
    await expect(page.getByTestId('home-empty')).toBeVisible();
  });

  test('已有成功结果、更新失败：保留旧值并显式标记过期；恢复后取消过期', async ({ page }) => {
    const st = await api(page, located, locatedAssets, { summary: (c) => (c.n === 2 ? 'fail' : 'ok') });
    await page.goto('/#/overview?view=2d');
    await expect(kpi(page, 'registered')).toHaveText('3台');
    await expect.poll(() => st.summaryCalls.length).toBe(1);
    const first = (await asof(page).textContent())!;
    await page.getByTestId('home-refresh').click(); // 2nd summary fails
    await expect(page.getByTestId('home-kpis')).toHaveAttribute('data-state', 'stale');
    await expect(kpi(page, 'registered')).toHaveText('3台'); // old value kept…
    await expect(page.locator('[data-kpi=registered] .home-stale-tag')).toHaveText('已过期'); // …and marked
    await expect(page.getByTestId('kpi-stale')).toBeVisible();
    await expect(page.getByTestId('home-attention')).toHaveAttribute('data-state', 'stale');
    await expect(asof(page)).toContainText('数据已过期');
    await expect(asof(page)).toContainText(first.match(/\d{4}-\d\d-\d\d \d\d:\d\d:\d\d/)![0]); // sampling time of the last SUCCESS
    await expect(page.getByTestId('home-load-error')).toContainText('已过期');
    await page.getByTestId('home-load-error').getByRole('button', { name: '重试' }).click(); // 3rd ok
    await expect(page.getByTestId('home-kpis')).toHaveAttribute('data-state', 'ready');
    await expect(page.locator('.home-stale-tag')).toHaveCount(0);
    await expect(page.getByTestId('home-load-error')).toHaveCount(0);
  });

  test('地图数据（Inventory）失败不影响权威指标，单独提示且可重试', async ({ page }) => {
    await api(page, located, locatedAssets, { projectsResponse: (n) => (n === 1 ? 'fail' : 'ok') });
    await page.goto('/#/overview?view=2d');
    await expect(page.getByTestId('home-kpis')).toHaveAttribute('data-state', 'ready');
    await expect(kpi(page, 'registered')).toHaveText('3台');
    await expect(page.getByTestId('map-load-error')).toContainText('指标不受影响');
    await expect(page.getByTestId('home-load-error')).toHaveCount(0);
    await page.getByTestId('map-load-error').getByRole('button', { name: '重试地图数据' }).click();
    await expect(page.getByTestId('map-load-error')).toHaveCount(0);
  });
});

// ---------------------------------------------------------------- R1-2
test.describe('R1-2 统计范围契约', () => {
  test('demo：筛选匿名登记项目后，告警 KPI、Attention 与 Inspector 告警列表数量一致', async ({ page }) => {
    await page.goto('http://127.0.0.1:5173/#/overview?view=2d');
    await expect(page.getByTestId('kpi-scope')).toContainText('全部展示');
    const all = Number(await kpi(page, 'alarm').getAttribute('data-value'));
    expect(all).toBeGreaterThan(1); // demo fixtures carry several alarms
    await page.getByRole('button', { name: '匿名登记项目', exact: true }).click();
    await expect(page.getByTestId('kpi-scope')).toContainText('匿名登记项目');
    const scoped = Number(await kpi(page, 'alarm').getAttribute('data-value'));
    expect(scoped).toBeLessThan(all); // the KPI follows the source filter…
    await expect(attention(page, 'alarm')).toHaveText(String(scoped)); // …Attention too
    await page.locator('[data-kpi=alarm]').click();
    await expect(page.getByTestId('inspector-total')).toHaveAttribute('data-total', String(scoped)); // …and the list
    await expect(page.locator('[data-testid=home-inspector] .inspector-device')).toHaveCount(scoped);
    // The status filter does not filter the KPIs themselves.
    expect(Number(await kpi(page, 'devices').getAttribute('data-value'))).toBeGreaterThan(scoped);
  });

  test('H2-1D 区域统计来自服务端 ?region=：省内一台 REAL 告警、其他省还有告警时数值切到当前省', async ({ page }) => {
    const projects: Proj[] = [
      { id: 'ZJ', name: '浙江项目', adminCode: 330481, devices: 1 },
      { id: 'GD', name: '广东项目', adminCode: 440300, devices: 2 },
    ];
    // The server decides per region; the browser holds every asset but must not filter them itself.
    const st = await api(page, projects, [
      { id: 'ZJ-1', projectId: 'ZJ', alarm: true },
      { id: 'GD-1', projectId: 'GD', alarm: true },
      { id: 'GD-2', projectId: 'GD', alarm: true },
    ], { summary: () => 'ok', metrics: {} });
    await page.unroute('**/api/v1/operations/summary*');
    const byRegion: Record<string, number> = { country: 3, '330000': 1, '440000': 2 };
    st.summaryCalls = await routeSummary(page, (c) =>
      inventorySummary(projects, [{ projectId: 'ZJ' }, { projectId: 'GD' }, { projectId: 'GD' }], c.region, { alarmReal: byRegion[c.region ?? 'country'] ?? 0 })
    );
    await page.goto('/#/overview?view=2d');
    await expect(kpi(page, 'alarm-real')).toHaveText('3台');
    await expect(page.getByTestId('kpi-scope')).toContainText('全国');
    await page.goto('/#/overview?view=2d&region=330000');
    await expect(main(page)).toHaveAttribute('data-adcode', '330000');
    await expect(page.getByTestId('kpi-scope')).toContainText('浙江省');
    await expect(kpi(page, 'alarm-real')).toHaveText('1台');
    await expect(kpi(page, 'registered')).toHaveText('1台');
    expect(st.summaryCalls.some((c) => c.region === 330000)).toBe(true);
    await expect(page.getByTestId('region-partition')).toHaveAttribute('data-registered', '1');
    // Back to the country: label and numbers follow (country = no region parameter).
    await page.getByRole('button', { name: '返回全国' }).click();
    await expect(page.getByTestId('kpi-scope')).toContainText('全国');
    await expect(kpi(page, 'alarm-real')).toHaveText('3台');
    expect(st.summaryCalls.filter((c) => c.region === null).length).toBeGreaterThanOrEqual(1);
  });

  test('切换地区与来源：KPI 标签与数值同步（demo，三维）', async ({ page }) => {
    await page.goto('http://127.0.0.1:5173/#/overview');
    await expect(page.locator('.province-summary')).toHaveCount(34);
    const nation = Number(await kpi(page, 'devices').getAttribute('data-value'));
    await page.goto('http://127.0.0.1:5173/#/overview?region=330000');
    await expect(main(page)).toHaveAttribute('data-adcode', '330000', { timeout: 20000 });
    await expect(page.getByTestId('kpi-scope')).toContainText('浙江省');
    const zj = Number(await kpi(page, 'devices').getAttribute('data-value'));
    expect(zj).toBeGreaterThan(0);
    expect(zj).toBeLessThan(nation);
    // Zhejiang holds only registered projects in the demo dataset: the registered
    // source keeps every device, the demo source leaves none — both labelled.
    await page.getByRole('button', { name: '匿名登记项目', exact: true }).click();
    await expect(page.getByTestId('kpi-scope')).toContainText('匿名登记项目 · 浙江省');
    await expect(kpi(page, 'devices')).toHaveAttribute('data-value', String(zj));
    await expect(kpi(page, 'planned')).toHaveAttribute('data-value', String(zj)); // registered devices in scope
    await page.getByRole('button', { name: '演示设备', exact: true }).click();
    await expect(page.getByTestId('kpi-scope')).toContainText('演示设备 · 浙江省');
    await expect(kpi(page, 'devices')).toHaveAttribute('data-value', '0');
    await expect(kpi(page, 'planned')).toHaveAttribute('data-value', '0');
    // Back to the country with the demo source: the national demo count.
    await page.getByRole('button', { name: '返回全国' }).first().click();
    await expect(page.getByTestId('kpi-scope')).toContainText('演示设备 · 全国');
    const demoNation = Number(await kpi(page, 'devices').getAttribute('data-value'));
    expect(demoNation).toBeGreaterThan(0);
    expect(demoNation).toBeLessThan(nation);
  });

  test('设备超过 60 台：Inspector 总数不被列表截断，并明确显示截断', async ({ page }) => {
    const assets: Asset[] = Array.from({ length: 75 }, (_, i) => ({ id: `BIG-${i + 1}`, projectId: 'P001', alarm: true }));
    await api(page, [{ id: 'P001', name: 'Big', adminCode: 330481, devices: 75 }], assets);
    await page.goto('/#/overview?view=2d&status=ALARM');
    await expect(kpi(page, 'registered')).toHaveText('75台');
    await expect(page.getByTestId('inspector-total')).toHaveAttribute('data-total', '75');
    await expect(page.getByTestId('inspector-total')).toContainText('75 台');
    await expect(page.locator('[data-testid=home-inspector] .inspector-device')).toHaveCount(60);
    await expect(page.getByTestId('inspector-cap')).toContainText('共 75 台');
  });

  test('Inventory KPI 点击进入同范围的 Inventory 设备清单，数量一致', async ({ page }) => {
    const projects: Proj[] = [
      { id: 'ZJ', name: '浙江项目', adminCode: 330481, devices: 2 },
      { id: 'GD', name: '广东项目', adminCode: 440300, devices: 1 },
    ];
    await api(page, projects, [
      { id: 'ZJ-1', projectId: 'ZJ' },
      { id: 'ZJ-2', projectId: 'ZJ' },
      { id: 'GD-1', projectId: 'GD' },
    ]);
    await page.goto('/#/overview?view=2d');
    await page.locator('[data-kpi=registered]').click();
    await expect(page).toHaveURL(/#\/devices$/);
    await expect(page.getByTestId('inventory-asset')).toHaveCount(3);
    await page.goto('/#/overview?view=2d&region=330000');
    await expect(kpi(page, 'registered')).toHaveText('2台');
    await page.locator('[data-kpi=registered]').click();
    await expect(page).toHaveURL(/#\/devices\?region=330000/);
    await expect(page.getByTestId('inventory-asset')).toHaveCount(2);
    await expect(page.getByTestId('inventory-scope')).toContainText('浙江省');
  });
});

test('R1-2 /devices?region= 只收窄授权数据：不扩大授权、与 ?project= 叠加、后退回首页、异常参数不显示假 0', async ({ page }) => {
  // The session is only authorized for ZJ and GD; the API (server-side) returns only those.
  const projects: Proj[] = [
    { id: 'ZJ', name: '浙江项目', adminCode: 330481, devices: 2 },
    { id: 'GD', name: '广东项目', adminCode: 440300, devices: 1 },
    { id: 'NOLOC', name: '无位置项目', adminCode: null, devices: 1 },
  ];
  await api(page, projects, [
    { id: 'ZJ-1', projectId: 'ZJ' },
    { id: 'ZJ-2', projectId: 'ZJ' },
    { id: 'GD-1', projectId: 'GD' },
    { id: 'NL-1', projectId: 'NOLOC' },
  ]);
  const rows = page.getByTestId('inventory-asset');
  // Original list query unchanged.
  await page.goto('/#/devices');
  await expect(rows).toHaveCount(4);
  await page.goto('/#/devices?project=GD');
  await expect(rows).toHaveCount(1);
  // Region narrows; combined with project it is an intersection (never a union).
  await page.goto('/#/devices?region=330000');
  await expect(rows).toHaveCount(2);
  await page.goto('/#/devices?region=330000&project=GD');
  await expect(rows).toHaveCount(0);
  await expect(page.getByTestId('inventory-region-empty')).toContainText('浙江省');
  // A region with no authorized project shows an explained empty list, not other projects.
  await page.goto('/#/devices?region=110000');
  await expect(rows).toHaveCount(0);
  await expect(page.getByTestId('inventory-region-empty')).toContainText('北京市');
  // Unknown region: explicit error, no count.
  await page.goto('/#/devices?region=999999');
  await expect(page.getByTestId('inventory-region-error')).toContainText('不存在');
  await expect(page.getByTestId('inventory-scope')).toContainText('— 台资产');
  await expect(rows).toHaveCount(0);
  // Malformed region: ignored with a notice, full authorized list.
  await page.goto('/#/devices?region=abc');
  await expect(page.getByTestId('inventory-region-ignored')).toBeVisible();
  await expect(rows).toHaveCount(4);
  // Browser history: home → KPI → /devices?region → Back returns to the same home scope.
  await page.goto('/#/overview?view=2d&region=330000');
  await expect(kpi(page, 'registered')).toHaveText('2台');
  await page.locator('[data-kpi=registered]').click();
  await expect(page).toHaveURL(/#\/devices\?region=330000/);
  await page.goBack();
  await expect(page).toHaveURL(/#\/overview\?.*region=330000/);
  await expect(page.getByTestId('kpi-scope')).toContainText('浙江省');
  await page.goForward();
  await expect(rows).toHaveCount(2);
});

test('R1-2 /devices?region= 行政区清单不可用：报错且不显示任何设备或 0', async ({ page }) => {
  await api(page, located, locatedAssets);
  await page.route('**/geo/geo-manifest.json', (r) => r.fulfill({ status: 500, body: 'x' }));
  await page.goto('/#/devices?region=330000');
  await expect(page.getByTestId('inventory-region-error')).toContainText('行政区数据不可用');
  await expect(page.getByTestId('inventory-asset')).toHaveCount(0);
  await expect(page.getByTestId('inventory-scope')).toContainText('— 台资产');
});

// ---------------------------------------------------------------- R1-3
test('R1-3/H2-1D-5 页头显示服务端取样时间（上海），标注三种时间；Cloud Runtime 独立轮询被明确区分', async ({ page }) => {
  await api(page, located, locatedAssets);
  await page.goto('/#/overview?view=2d');
  await expect(asof(page)).toHaveAttribute('data-state', 'ready');
  // Fixture sampledAt 2026-11-09T04:05:51Z → 12:05:51 Shanghai; generatedAt …04:05:51.394Z.
  await expect(asof(page)).toContainText('统计取样 2026-11-09 12:05:51 · 每 60 秒更新');
  await expect(asof(page)).not.toContainText('数据截至');
  await expect(asof(page)).toHaveAttribute('title', /统计取样 sampledAt 2026-11-09 12:05:51/);
  await expect(asof(page)).toHaveAttribute('title', /生成完成 generatedAt 2026-11-09 12:05:51/);
  await expect(asof(page)).toHaveAttribute('title', /浏览器接收 \d{4}-\d\d-\d\d \d\d:\d\d:\d\d/);
  await expect(asof(page)).toHaveAttribute('title', /不是设备遥测的观测时间/);
  await expect(page.getByTestId('cloud-runtime-cycle')).toContainText('不刷新首页指标');
  // Cloud Runtime sits after the homepage sections, not between title and KPIs.
  const order = await page.evaluate(() => {
    const kpis = document.querySelector('[data-testid=home-kpis]')!;
    const rt = document.querySelector('[data-testid=cloud-runtime]')!;
    return kpis.compareDocumentPosition(rt) & Node.DOCUMENT_POSITION_FOLLOWING;
  });
  expect(order).toBeTruthy();
});

// ---------------------------------------------------------------- R1-4
async function inspectorRect(page: Page) {
  return (await page.getByTestId('home-inspector').boundingBox())!;
}
async function stageRect(page: Page) {
  return (await page.getByTestId('spatial-stage').boundingBox())!;
}
async function focusRect(page: Page) {
  return page.evaluate(() => {
    const d = (window as unknown as { __spatialDiagnostics?: () => { focusedScreenRect: { left: number; top: number; right: number; bottom: number } | null } }).__spatialDiagnostics;
    const r = typeof d === 'function' ? d()?.focusedScreenRect : null;
    if (!r) return null;
    const host = document.querySelector('.map-canvas')!.getBoundingClientRect();
    return { left: r.left + host.left, right: r.right + host.left, top: r.top + host.top, bottom: r.bottom + host.top };
  });
}

for (const [w, h] of [
  [1280, 720],
  [1440, 900],
] as const) {
  test(`R1-4 ${w}×${h} 三维选中资产：标记在抽屉外的可见区域，地图工具可点击`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: h });
    await api(page, located, locatedAssets);
    await page.goto('/#/overview?asset=A-1');
    await expect(page.getByTestId('device-panel')).toHaveAttribute('data-device-id', 'A-1');
    await expect(main(page)).toHaveAttribute('data-camera-preset', 'DEVICE_FOCUS', { timeout: 20000 });
    await expect(main(page)).toHaveAttribute('data-scene-state', 'PROJECT_FOCUS');
    await page.waitForTimeout(600); // camera settle (setInspectorWidth reframe)
    const ins = await inspectorRect(page);
    const stage = await stageRect(page);
    const r = (await focusRect(page))!;
    expect(r, 'focused marker projected').not.toBeNull();
    const cx = (r.left + r.right) / 2,
      cy = (r.top + r.bottom) / 2;
    expect(cx, '选中标记中心在舞台内').toBeGreaterThan(stage.x);
    expect(cx, '选中标记中心不在抽屉下').toBeLessThan(ins.x - 8);
    expect(cy).toBeGreaterThan(stage.y);
    expect(cy).toBeLessThan(stage.y + stage.height);
    // The camera frames the target in the UNCOVERED part of the stage (stage left
    // edge → drawer left edge), not the whole stage behind the drawer.
    const uncoveredCenter = (stage.x + ins.x) / 2;
    expect(Math.abs(cx - uncoveredCenter), `选中标记居中于未遮挡区域（中心 ${uncoveredCenter.toFixed(0)}，实际 ${cx.toFixed(0)}）`).toBeLessThan(60);
    for (const name of ['重置全国视角', '聚焦首个登记项目']) {
      const box = (await page.getByRole('button', { name }).boundingBox())!;
      const hit = await page.evaluate(
        ({ x, y, name }) => document.elementFromPoint(x, y)?.closest('button')?.getAttribute('aria-label') === name,
        { x: box.x + box.width / 2, y: box.y + box.height / 2, name }
      );
      expect(hit, `${name} 可点击`).toBe(true);
    }
    await page.screenshot({ path: test.info().outputPath(`r1-select-${w}x${h}-3d.png`) });
    await test.info().attach(`r1-select-${w}x${h}-3d`, { path: test.info().outputPath(`r1-select-${w}x${h}-3d.png`), contentType: 'image/png' });
  });

  test(`R1-4 ${w}×${h} 二维选中资产：所在区域高亮且在抽屉外`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: h });
    await api(page, located, locatedAssets);
    await page.goto('/#/overview?view=2d&region=330000&asset=A-1');
    await expect(page.getByTestId('device-panel')).toHaveAttribute('data-device-id', 'A-1');
    const sel = page.locator('.stage2d-map path.selected');
    await expect(sel).toHaveCount(1);
    await expect(sel).toHaveAttribute('data-region-code', '330400');
    const box = (await sel.boundingBox())!;
    const ins = await inspectorRect(page);
    expect(box.x + box.width / 2, '高亮区域中心不在抽屉下').toBeLessThan(ins.x - 8);
    await page.screenshot({ path: test.info().outputPath(`r1-select-${w}x${h}-2d.png`) });
    await test.info().attach(`r1-select-${w}x${h}-2d`, { path: test.info().outputPath(`r1-select-${w}x${h}-2d.png`), contentType: 'image/png' });
  });
}

for (const view of ['3d', '2d'] as const)
  test(`R1-4 Esc 顺序（${view}）：先关详情→再上退一级；URL、Inspector、地图层级一致；只有「返回全国」直接重置`, async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await api(page, located, locatedAssets);
    const v = view === '2d' ? '&view=2d' : '';
    await page.goto(`/#/overview?region=330400${v}`);
    await expect(main(page)).toHaveAttribute('data-adcode', '330400', { timeout: 20000 });
    await expect(main(page)).toHaveAttribute('data-scene-state', 'REGION_IDLE');
    await page.locator('[data-testid=home-inspector] .plan-item[data-project-id=P001]').click();
    await expect(page.getByTestId('project-panel')).toHaveAttribute('data-project-id', 'P001');
    await expect(page).toHaveURL(/project=P001/);
    if (view === '3d') await expect(main(page)).toHaveAttribute('data-scene-state', 'PROJECT_FOCUS', { timeout: 20000 });
    // 1st Esc: detail closed, region unchanged (NOT the country).
    await page.keyboard.press('Escape');
    await expect(page.getByTestId('project-panel')).toHaveCount(0);
    await expect.poll(() => page.url()).not.toContain('project=');
    await expect(main(page)).toHaveAttribute('data-adcode', '330400', { timeout: 20000 });
    await expect(main(page)).toHaveAttribute('data-scene-state', 'REGION_IDLE', { timeout: 20000 });
    await expect(page).toHaveURL(/region=330400/);
    // 2nd Esc: up one level to the province.
    await page.keyboard.press('Escape');
    await expect(main(page)).toHaveAttribute('data-adcode', '330000', { timeout: 20000 });
    await expect(page).toHaveURL(/region=330000/);
    await expect(page.getByTestId('kpi-scope')).toContainText('浙江省');
    // Explicit "返回全国" resets to the country directly.
    await page.goto(`/#/overview?region=330481${v}&project=P001`);
    await expect(page.getByTestId('project-panel')).toHaveAttribute('data-project-id', 'P001');
    await page.getByRole('button', { name: '返回全国' }).first().click();
    await expect(main(page)).toHaveAttribute('data-adcode', '100000', { timeout: 20000 });
    await expect.poll(() => page.url()).not.toContain('region=');
  });

// ---------------------------------------------------------------- R1-5
test('R1-5 WebGL 上下文丢失：可恢复时恢复三维；未恢复则自动切到轻量视图，舞台不空白', async ({ page }) => {
  await api(page, located, locatedAssets);
  await page.goto('/#/overview');
  await expect(page.locator('.province-summary')).toHaveCount(34);
  const lose = () =>
    page.evaluate(() => {
      const gl = (document.querySelector('.map-canvas canvas') as HTMLCanvasElement).getContext('webgl2')!;
      const ext = gl.getExtension('WEBGL_lose_context')!;
      (window as unknown as { __lose: WEBGL_lose_context }).__lose = ext;
      ext.loseContext();
    });
  // Recoverable: lost then restored within the grace period → stays 3D.
  await lose();
  await expect(page.getByTestId('scene-context-lost')).toBeVisible();
  await page.evaluate(() => (window as unknown as { __lose: WEBGL_lose_context }).__lose.restoreContext());
  await expect(page.getByTestId('scene-context-lost')).toHaveCount(0);
  await page.waitForTimeout(3500);
  await expect(main(page)).toHaveAttribute('data-view', '3d');
  await expect(page.getByTestId('webgl-fallback')).toHaveCount(0);
  // Not restored: fall back to the 2D view with an explanation; business still works.
  await lose();
  await expect(page.getByTestId('webgl-fallback')).toBeVisible({ timeout: 8000 });
  await expect(main(page)).toHaveAttribute('data-view', '2d');
  await expect(page.locator('.stage2d-map path').first()).toBeVisible();
  await expect(page.getByRole('button', { name: '三维沙盘' })).toBeDisabled();
  await expect(kpi(page, 'registered')).toHaveText('3台'); // numbers unaffected by the renderer
  await page.locator('[data-kpi=pending-review]').click(); // KPI filter still works on the 2D fallback
  await expect(main(page)).toHaveAttribute('data-status-filter', 'PENDING_REVIEW');
  await expect(page.getByTestId('review-list')).toBeVisible();
});

test('R1-5 地图资源加载失败：三维显示可恢复错误，重试成功；也可切到轻量视图（不永久空白）', async ({ page }) => {
  await api(page, located, locatedAssets);
  let failChina = 1;
  await page.route('**/geo/china/100000.json', (r) => (failChina-- > 0 ? r.fulfill({ status: 500, body: 'x' }) : r.fallback()));
  await page.goto('/#/overview');
  await expect(page.getByTestId('scene-resource-error')).toBeVisible();
  await expect(page.getByTestId('scene-resource-error')).toContainText('地图资源加载失败');
  await page.getByTestId('scene-resource-error').getByRole('button', { name: '重试' }).click();
  await expect(page.locator('.province-summary')).toHaveCount(34);
  await expect(page.getByTestId('scene-resource-error')).toHaveCount(0);
  // Permanent failure → light view offered and working.
  await page.unrouteAll({ behavior: 'ignoreErrors' });
  await api(page, located, locatedAssets);
  await page.route('**/geo/china/100000.json', (r) => r.fulfill({ status: 500, body: 'x' }));
  await page.route('**/geo/provinces/330000.json', (r) => r.fallback());
  await page.goto('/#/overview?region=330000');
  await page.reload();
  await expect(page.getByTestId('scene-resource-error')).toBeVisible();
  await page.getByTestId('scene-resource-error').getByRole('button', { name: '切换轻量视图' }).click();
  await expect(main(page)).toHaveAttribute('data-view', '2d');
  await expect(page.locator('.stage2d-map path').first()).toBeVisible();
});

// ---------------------------------------------------------------- R1-6
for (const [w, h] of [
  [1280, 720],
  [1366, 768],
  [1440, 900],
] as const)
  test(`R1-6 ${w}×${h} 选中后 Inspector 打开时的实际可用地图区域`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: h });
    await api(page, located, locatedAssets);
    await page.goto('/#/overview?project=P001');
    await expect(page.getByTestId('project-panel')).toHaveAttribute('data-project-id', 'P001');
    await expect(main(page)).toHaveAttribute('data-scene-state', 'PROJECT_FOCUS', { timeout: 20000 });
    const m = await page.evaluate(() => {
      const stage = document.querySelector('[data-testid=spatial-stage]')!.getBoundingClientRect();
      const ins = document.querySelector('[data-testid=home-inspector]')!.getBoundingClientRect();
      const vw = innerWidth,
        vh = innerHeight;
      const x0 = Math.max(0, stage.left),
        y0 = Math.max(0, stage.top),
        y1 = Math.min(vh, stage.bottom);
      const x1 = Math.min(vw, stage.right, ins.left); // the drawer overlays the stage's right edge
      let area = Math.max(0, x1 - x0) * Math.max(0, y1 - y0);
      for (const el of document.querySelectorAll(
        '[data-testid=spatial-stage] .map-topline > *, [data-testid=spatial-stage] .map-view-switch, [data-testid=spatial-stage] .observation-switch, [data-testid=spatial-stage] .scene-mode-dock, [data-testid=spatial-stage] .map-tools, [data-testid=spatial-stage] .map-attribution, [data-testid=spatial-stage] .south-sea-inset-note, [data-testid=spatial-stage] .stage2d-legend'
      )) {
        const r = el.getBoundingClientRect();
        area -= Math.max(0, Math.min(x1, r.right) - Math.max(x0, r.left)) * Math.max(0, Math.min(y1, r.bottom) - Math.max(y0, r.top));
      }
      return { usableMapPct: +((area / (vw * vh)) * 100).toFixed(1), usableWidth: Math.round(x1 - x0), usableHeight: Math.round(y1 - y0) };
    });
    writeFileSync(test.info().outputPath(`inspector-usable-${w}x${h}.json`), JSON.stringify({ viewport: `${w}x${h}`, ...m }));
    await test.info().attach(`inspector-usable-${w}x${h}`, { body: JSON.stringify(m), contentType: 'application/json' });
    await page.screenshot({ path: test.info().outputPath(`r1-inspector-${w}x${h}.png`) });
    await test.info().attach(`r1-inspector-${w}x${h}`, { path: test.info().outputPath(`r1-inspector-${w}x${h}.png`), contentType: 'image/png' });
    // Floor, not a target: the drawer may cover at most the stage's right third,
    // so at least ~600 px × 400 px of map stays usable at every narrow width.
    expect(m.usableWidth).toBeGreaterThanOrEqual(600);
    expect(m.usableHeight).toBeGreaterThanOrEqual(400);
  });
