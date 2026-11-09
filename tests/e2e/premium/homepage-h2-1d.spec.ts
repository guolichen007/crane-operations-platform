// H2-1D acceptance: the Premium homepage shows the authoritative operations summary (real browser,
// mocked API — no server writes). Each test asserts the observable numbers, not just that a banner
// exists; counter-proofs (revert one rule → this file fails) are recorded in
// docs/04-前端与可视化/Homepage-H2-1D-首页接入权威运营统计.md.
import { test, expect, type Page } from '@playwright/test';
import { cloudDevice, cloudAlarm } from '../../../apps/web-console/tests/cloud-fixtures';
import { summary, GEO_VERSION } from '../../../apps/web-console/tests/summary-fixtures';
import { inventorySummary, routeSummary, type SummaryReply, type SummaryCall } from '../summary-mock';

type Grant = { projectId: string; projectName: string; role: string };
const grantsP1: Grant[] = [{ projectId: 'P001', projectName: 'Project-A', role: 'viewer' }];
function sessionOf(userId: string, projects: Grant[]) {
  return { userId, displayName: userId, projects, csrfToken: 'synthetic-csrf-test-only', expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' };
}
const projects = [
  { id: 'P001', name: 'Project-A', lifecycle: 'PLANNED', province: '浙江省', city: '', county: '', locationLabel: '', adminCode: 330481, scheduleText: '', notes: '', simulatedCount: 0, runtimeDeviceCount: 2, realConnectedCount: 0, assetDeviceCount: 2 },
];
// The map's Shadow says A-1 is a CONNECTED **SIMULATION** identity with an alarm and A-2 a REAL identity
// that is OFFLINE. A client-side derivation would show neither as "REAL 在线" — but the server is the
// only authority: whatever the summary says is what the cards must show.
const assets = [
  { assetDeviceId: 'A-1', projectId: 'P001', displayName: 'A-1', craneModel: null, ratedCapacityT: null, spanM: null, workClass: null, grabType: null, lifecycle: 'PLANNED', runtimeSource: 'SIMULATION', runtimeDeviceId: 'SIM-A1', connectivity: 'MQTT_CONNECTED' },
  { assetDeviceId: 'A-2', projectId: 'P001', displayName: 'A-2', craneModel: null, ratedCapacityT: null, spanM: null, workClass: null, grabType: null, lifecycle: 'PLANNED', runtimeSource: 'REAL', runtimeDeviceId: 'EDGE-A2', connectivity: 'OFFLINE' },
];
function shadows() {
  const sim = cloudDevice('SIM-A1');
  sim.reported.alarmState = 'ACTIVE';
  sim.reported.operatingState = 'LIVE';
  const real = cloudDevice('EDGE-A2');
  real.presence.connectivity = 'OFFLINE';
  return [sim, real];
}

/** Inventory/Shadow map data + a programmable summary. `session` can be swapped during the test. */
async function setup(page: Page, reply: (c: SummaryCall) => SummaryReply | Promise<SummaryReply>, who = { user: 'u1', grants: grantsP1 }) {
  const state = { who };
  await page.route('**/auth/session', (r) => r.fulfill({ json: sessionOf(state.who.user, state.who.grants) }));
  await page.route('**/api/v1/**', (r) => {
    const path = new URL(r.request().url()).pathname;
    return r.fulfill({
      json: path === '/api/v1/projects' ? projects : path === '/api/v1/assets/devices' ? assets : path === '/api/v1/devices' ? shadows() : path === '/api/v1/alarms' ? [cloudAlarm()] : {},
    });
  });
  const calls = await routeSummary(page, reply);
  return { calls, state };
}
const kpi = (page: Page, id: string) => page.locator(`[data-kpi=${id}] .home-kpi-value`);
const attention = (page: Page, id: string) => page.locator(`[data-attention=${id}] b`);
const kpis = (page: Page) => page.getByTestId('home-kpis');
const main = (page: Page) => page.locator('main.overview');

test('1/2/3 五张 KPI 精确等于 summary 字段；REAL 在线不从本地 Shadow 推算（REAL 离线、SIM 在线 ≠ REAL 在线）', async ({ page }) => {
  // Deliberately "impossible" for the local Shadow: the server says 4 REAL connected while the map's
  // Shadow has no connected REAL identity. The card must show the server's 4, not a local 0.
  await setup(page, () =>
    summary({ registered: 37, realConnected: 4, alarmReal: 2, pendingReview: 3, noRuntime: 11, boundReal: 9, boundSim: 20, offline: 5, stale: 2, unknownTime: 1, pendingReviewAlarm: 1, unlocated: 6 })
  );
  await page.goto('/#/overview?view=2d');
  await expect(kpis(page)).toHaveAttribute('data-state', 'ready');
  const expected = { registered: '37台', 'real-connected': '4台', 'alarm-real': '2台', 'pending-review': '3台', 'no-runtime': '11台' };
  for (const [id, v] of Object.entries(expected)) await expect(kpi(page, id)).toHaveText(v);
  await expect(page.locator('[data-kpi=real-connected] .home-kpi-label')).toHaveText('REAL 在线');
  await expect(kpis(page)).not.toContainText('真实运行');
  await expect(page.locator('[data-kpi]')).toHaveCount(5);
  // Attention: independent fields, each its own number.
  for (const [id, v] of Object.entries({ 'pending-review-alarm': '1', 'real-offline': '5', 'real-stale': '2', 'real-unknown-time': '1', unlocated: '6' }))
    await expect(attention(page, id)).toHaveText(v);
  // Operation (作业) is still not available — LIVE is never shown as working.
  await expect(page.getByTestId('home-attention')).not.toContainText('作业中');
});

test('3 REAL 离线 + SIM 在线告警：服务端 REAL 在线 0、REAL 告警 0，卡片显示 0（SIM 不计入）', async ({ page }) => {
  await setup(page, () => summary({ registered: 2, boundReal: 1, boundSim: 1, realConnected: 0, offline: 1, alarmReal: 0, alarmSim: 1, recordsTotal: 3 }));
  await page.goto('/#/overview?view=2d');
  await expect(kpi(page, 'real-connected')).toHaveText('0台');
  await expect(kpi(page, 'alarm-real')).toHaveText('0台');
  await expect(attention(page, 'real-offline')).toHaveText('1');
  await expect(page.locator('[data-kpi=real-connected]')).toContainText('REAL 绑定 1 · SIM 绑定 1（SIM 不计入）');
});

test('4 当前告警 0、历史 150 条：卡片 0，时间窗总数 150（不是最近 100 条明细长度）；切换时间窗重新请求', async ({ page }) => {
  const { calls } = await setup(page, (c) => summary({ alarmReal: 0, recordsTotal: c.window === 'P7D' ? 640 : 150, window: c.window as 'PT24H' }));
  await page.goto('/#/overview?view=2d');
  await expect(kpi(page, 'alarm-real')).toHaveText('0台');
  await expect(page.locator('[data-kpi=alarm-real]')).toContainText('近 24 小时告警记录 150 条');
  await expect(page.getByTestId('event-records')).toContainText('近 24 小时告警记录 150 条（服务端计数 · 非未处理数）');
  await page.getByTestId('alarm-window').selectOption('P7D');
  await expect(page.locator('[data-kpi=alarm-real]')).toContainText('近 7 天告警记录 640 条');
  expect(calls.map((c) => c.window)).toContain('P7D');
});

test('5 首次 503：显示不可用，任何 KPI/关注项都没有数字（不退化为 0）', async ({ page }) => {
  await setup(page, () => 'fail');
  await page.goto('/#/overview?view=2d');
  await expect(kpis(page)).toHaveAttribute('data-state', 'unavailable');
  for (const id of ['registered', 'real-connected', 'alarm-real', 'pending-review', 'no-runtime']) await expect(kpi(page, id)).toHaveText('不可用');
  await expect(kpis(page)).not.toContainText(/\d+台/);
  await expect(page.getByTestId('home-attention').locator('button b, .attention-row.info b')).not.toContainText([/\d/]);
  await expect(page.getByTestId('home-empty')).toHaveCount(0);
  await expect(page.getByTestId('region-partition')).toContainText('区域统计不可用');
});

test('6 同范围刷新失败：保留旧值并标注过期，取样时间保持上次成功', async ({ page }) => {
  const { calls } = await setup(page, (c) => (c.n === 2 ? 'fail' : summary({ registered: 12, sampledAt: c.n === 1 ? '2026-11-09T01:00:00Z' : '2026-11-09T01:01:00Z' })));
  await page.goto('/#/overview?view=2d');
  await expect(page.getByTestId('home-asof')).toContainText('统计取样 2026-11-09 09:00:00');
  await expect.poll(() => calls.length).toBe(1);
  await page.getByTestId('home-refresh').click();
  await expect(kpis(page)).toHaveAttribute('data-state', 'stale');
  await expect(kpi(page, 'registered')).toHaveText('12台');
  await expect(page.locator('[data-kpi=registered] .home-stale-tag')).toBeVisible();
  await expect(page.getByTestId('home-asof')).toContainText('统计取样 2026-11-09 09:00:00 · 更新失败，数据已过期');
  await page.getByTestId('home-refresh').click();
  await expect(kpis(page)).toHaveAttribute('data-state', 'ready');
  await expect(page.getByTestId('home-asof')).toContainText('09:01:00');
});

test('7 浙江→广东→浙江请求乱序：只显示最后范围；广东失败不显示浙江数字', async ({ page }) => {
  // Zhejiang answers slowly the first time, Guangdong fails; every answer claims its own scope.
  let zj = 0;
  const { calls } = await setup(page, (c) => {
    if (c.region === 440000) return { delayMs: 300, body: summary({ region: 440000, regionName: '广东省', registered: 99 }) } as SummaryReply;
    if (c.region === 330000) {
      zj++;
      return { delayMs: zj === 1 ? 2500 : 100, body: summary({ region: 330000, regionName: '浙江省', registered: zj === 1 ? 11 : 21 }) } as SummaryReply;
    }
    return summary({ registered: 50 });
  });
  await page.goto('/#/overview?view=2d');
  await expect(kpi(page, 'registered')).toHaveText('50台');
  // Each region is really displayed (and its request sent) before the next switch, so the three
  // requests overlap: ZJ #1 is still in flight (2.5 s) when GD and then ZJ #2 are requested.
  await page.goto('/#/overview?view=2d&region=330000'); // slow ZJ #1 (11)
  await expect(main(page)).toHaveAttribute('data-adcode', '330000');
  await expect.poll(() => calls.filter((c) => c.region === 330000).length).toBe(1);
  await page.goto('/#/overview?view=2d&region=440000'); // GD (99, 0.3 s)
  await expect(main(page)).toHaveAttribute('data-adcode', '440000');
  await expect.poll(() => calls.filter((c) => c.region === 440000).length).toBe(1);
  await page.goto('/#/overview?view=2d&region=330000'); // ZJ #2 (21) — the only valid answer
  await expect(main(page)).toHaveAttribute('data-adcode', '330000');
  await expect.poll(() => calls.filter((c) => c.region === 330000).length).toBe(2);
  await expect(kpi(page, 'registered')).toHaveText('21台');
  await page.waitForTimeout(2800); // let the slow ZJ #1 and GD answers arrive late
  await expect(kpi(page, 'registered')).toHaveText('21台');
  await expect(page.getByTestId('kpi-scope')).toContainText('浙江省');
  expect(calls.filter((c) => c.region === 330000).length).toBeGreaterThanOrEqual(2);

  // Guangdong now fails: its numbers are unavailable — Zhejiang's 21 are NOT shown as "Guangdong, expired".
  await page.unroute('**/api/v1/operations/summary*');
  await routeSummary(page, (c) => (c.region === 440000 ? 'fail' : summary({ region: c.region, registered: c.region === 330000 ? 21 : 50 })));
  await page.goto('/#/overview?view=2d&region=440000');
  await expect(page.getByTestId('kpi-scope')).toContainText('广东省');
  await expect(kpis(page)).toHaveAttribute('data-state', 'unavailable');
  await expect(kpi(page, 'registered')).toHaveText('不可用');
  await expect(kpis(page)).not.toContainText('21');
});

test('8 切换账号 / 撤销授权：旧统计立即失效，不显示上一账号的数字', async ({ page }) => {
  const { state } = await setup(page, (c) => {
    void c;
    return summary({ registered: state.who.user === 'u1' ? (state.who.grants.length > 1 ? 30 : 7) : 2 });
  }, { user: 'u1', grants: [...grantsP1, { projectId: 'P002', projectName: 'Project-B', role: 'viewer' }] });
  await page.goto('/#/overview?view=2d');
  await expect(kpi(page, 'registered')).toHaveText('30台');
  // Grant P002 revoked server-side: the next session read has fewer grants → new identity → cache dropped.
  state.who = { user: 'u1', grants: grantsP1 };
  await page.evaluate(() => window.dispatchEvent(new Event('cloud-session-expired')));
  await page.reload();
  await expect(kpi(page, 'registered')).toHaveText('7台');
  // Another account on the same browser: never shows u1's numbers, not even while loading.
  state.who = { user: 'u2', grants: [{ projectId: 'P009', projectName: 'Project-Z', role: 'viewer' }] };
  await page.unroute('**/api/v1/operations/summary*');
  await routeSummary(page, () => ({ delayMs: 1500, body: summary({ registered: 2 }) }) as SummaryReply);
  await page.reload();
  await expect(kpi(page, 'registered')).toHaveText('—');
  await expect(kpis(page)).not.toContainText('7台');
  await expect(kpi(page, 'registered')).toHaveText('2台');
});

test('8b 401：进入认证恢复流程（登录页），不显示 0', async ({ page }) => {
  let authed = true;
  await page.route('**/auth/session', (r) => (authed ? r.fulfill({ json: sessionOf('u1', grantsP1) }) : r.fulfill({ status: 401, json: {} })));
  await page.route('**/api/v1/**', (r) => r.fulfill({ json: [] }));
  await routeSummary(page, () => {
    authed = false;
    return { status: 401 };
  });
  await page.goto('/#/overview?view=2d');
  await expect(page.getByRole('link', { name: '账号登录' })).toBeVisible();
  await expect(page.locator('[data-kpi]')).toHaveCount(0);
});

test('9 无任何授权项目：合法显示 0 与空态', async ({ page }) => {
  await setup(page, () => summary({ authorizedProjects: 0, projects: 0 }), { user: 'u0', grants: [] });
  await page.goto('/#/overview?view=2d');
  await expect(kpis(page)).toHaveAttribute('data-state', 'ready');
  for (const id of ['registered', 'real-connected', 'alarm-real', 'pending-review', 'no-runtime']) await expect(kpi(page, id)).toHaveText('0台');
  await expect(page.getByTestId('home-empty')).toContainText('没有已授权的项目');
});

test('10 全国/省/市/县：每级向服务端请求 ?region=，KPI、范围标签、下级区域数与分区等式一致', async ({ page }) => {
  // P001 in Haining (330481) with 2 assets; P-HZ at the Jiaxing prefecture code itself (atScopeLevel).
  const inv = [
    { id: 'P001', adminCode: 330481 },
    { id: 'P-JX', adminCode: 330400 },
    { id: 'P-NL', adminCode: null },
  ];
  const invAssets = [{ projectId: 'P001' }, { projectId: 'P001' }, { projectId: 'P-JX' }, { projectId: 'P-NL' }];
  const { calls } = await setup(page, (c) => inventorySummary(inv, invAssets, c.region));
  await page.goto('/#/overview?view=2d');
  const part = page.getByTestId('region-partition');
  // Country: 4 = Zhejiang 3 + scope level 0 + unlocated 1.
  await expect(kpi(page, 'registered')).toHaveText('4台');
  await expect(part).toHaveText('登记 4 = 下级 3 + 本级 0 + 无位置 1');
  await expect(page.locator('[data-region-code="330000"] .region-count')).toHaveText('3');
  for (const [code, name, reg, text] of [
    ['330000', '浙江省', '3台', '登记 3 = 下级 3 + 本级 0'],
    ['330400', '嘉兴市', '3台', '登记 3 = 下级 2 + 本级 1'],
    ['330481', '海宁市', '2台', '登记 2 = 下级 0 + 本级 2'],
  ] as const) {
    await page.locator(`.stage2d-map path[data-region-code="${code}"]`).click();
    await expect(main(page)).toHaveAttribute('data-adcode', code);
    await expect(page.getByTestId('kpi-scope')).toContainText(name);
    await expect(kpi(page, 'registered')).toHaveText(reg);
    await expect(part).toHaveText(text);
    expect(calls.some((c) => c.region === Number(code))).toBe(true);
  }
  // Breadcrumb back to the country: region=null (no parameter), unlocated shown again.
  await page.getByRole('button', { name: '返回全国' }).click();
  await expect(kpi(page, 'registered')).toHaveText('4台');
  await expect(part).toHaveText('登记 4 = 下级 3 + 本级 0 + 无位置 1');
  expect(calls.every((c) => c.region !== 100000)).toBe(true);
});

test('10b 行政区版本不一致：显示可理解的错误，不显示区域数字', async ({ page }) => {
  await setup(page, () => summary({ registered: 9, geoIndexVersion: 'f'.repeat(64) }));
  await page.goto('/#/overview?view=2d');
  await expect(kpis(page)).toHaveAttribute('data-state', 'unavailable');
  await expect(page.getByTestId('home-load-error')).toContainText('行政区数据版本不一致');
  await expect(kpi(page, 'registered')).toHaveText('不可用');
  // The real manifest version matches the backend index (both sha256 of the same file).
  await page.unroute('**/api/v1/operations/summary*');
  await routeSummary(page, () => summary({ registered: 9, geoIndexVersion: GEO_VERSION }));
  await page.getByTestId('home-load-error').getByRole('button', { name: '重试' }).click();
  await expect(kpi(page, 'registered')).toHaveText('9台');
});

test('11 演示模式不调用生产 summary 接口', async ({ browser }) => {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  const hits: string[] = [];
  page.on('request', (r) => {
    if (r.url().includes('/api/v1/')) hits.push(r.url());
  });
  await page.goto('http://127.0.0.1:5173/#/overview?view=2d');
  await expect(page.locator('[data-kpi=devices]')).toBeVisible();
  await expect(page.locator('[data-kpi=devices] .home-kpi-tag')).toHaveText('DEMO');
  await page.waitForTimeout(1500);
  expect(hits.filter((u) => u.includes('operations/summary'))).toEqual([]);
  await ctx.close();
});

test('待复核：显示「前 N 条 / 共 M 台」，每条打开有权限的资产详情；不伪造「查看全部」', async ({ page }) => {
  const items = Array.from({ length: 100 }, (_, i) => ({ id: `ASSET-R${String(i).padStart(3, '0')}`, assetStatus: (i % 2 ? 'DRAFT' : 'OUT_OF_SERVICE') as 'DRAFT', alarm: i === 0 }));
  await setup(page, () => summary({ registered: 300, boundReal: 200, pendingReview: 135, pendingReviewAlarm: 1, pendingReviewAssets: items }));
  await page.goto('/#/overview?view=2d&status=PENDING_REVIEW');
  await expect(kpi(page, 'pending-review')).toHaveText('135台');
  await expect(page.getByTestId('review-cap')).toContainText('前 100 条 / 共 135 台');
  await expect(page.locator('.review-asset')).toHaveCount(100);
  await expect(page.getByTestId('review-list')).not.toContainText('查看全部');
  await expect(page.locator('.review-asset').first()).toHaveAttribute('href', '#/assets/ASSET-R000');
  await expect(page.locator('.review-asset').first()).toContainText('REAL 告警');
});

test('业务钻取：非精确 KPI 打开同范围 Inventory 清单并明确标注「不是精确筛选」；登记卡无此标注', async ({ page }) => {
  await setup(page, (c) => summary({ region: c.region, registered: 2, realConnected: 1 }));
  await page.goto('/#/overview?view=2d&region=330000');
  await expect(kpi(page, 'real-connected')).toHaveText('1台');
  await page.locator('[data-kpi=real-connected]').click();
  await expect(page).toHaveURL(/#\/devices\?region=330000&kpi=real-connected/);
  await expect(page.getByTestId('inventory-kpi-note')).toContainText('不是');
  await expect(page.getByTestId('inventory-kpi-note')).toContainText('REAL 在线');
  await page.goBack();
  await page.locator('[data-kpi=registered]').click();
  await expect(page).toHaveURL(/#\/devices\?region=330000$/);
  await expect(page.getByTestId('inventory-kpi-note')).toHaveCount(0);
  await expect(page.getByTestId('inventory-asset')).toHaveCount(2);
});

test('API 模式不再提供「登记项目」来源筛选（不把登记项目误读为 REAL 连接）', async ({ page }) => {
  await setup(page, () => summary({ registered: 2 }));
  await page.goto('/#/overview?view=2d');
  await expect(kpi(page, 'registered')).toHaveText('2台');
  await expect(page.getByRole('button', { name: '登记项目', exact: true })).toHaveCount(0);
  await expect(page.locator('.home-source')).toHaveCount(0);
});

test('60 秒自动刷新（Cloud Runtime 15 秒轮询独立，不刷新首页统计）', async ({ page }) => {
  await page.clock.install();
  const { calls } = await setup(page, (c) => summary({ registered: c.n }));
  await page.goto('/#/overview?view=2d');
  await expect(kpi(page, 'registered')).toHaveText('1台');
  const before = calls.length;
  await page.clock.runFor(30_000); // two Cloud Runtime cycles, no summary refresh
  expect(calls.length).toBe(before);
  await page.clock.runFor(31_000);
  await expect.poll(() => calls.length).toBe(before + 1);
  await expect(kpi(page, 'registered')).toHaveText(`${before + 1}台`);
  await expect(page.getByTestId('cloud-runtime-cycle')).toContainText('不刷新首页指标');
});
