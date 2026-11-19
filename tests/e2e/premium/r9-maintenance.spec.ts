// R9 B6 — first-asset tabs (台账与点位 / 运行监测 / 寿命与维护 / 历史), the work order flow with a
// replacement, the 维护与到期中心 filters and the home attention panel, in a real browser against
// a stateful mocked API that keeps the server rules (role gating, If-Match, SIM marking). The same
// rules against the real BFF + PostgreSQL: services/cloud-api/internal/app/r9_maintenance_http_test.go;
// against real Keycloak: tests/e2e/b4/lhk-first-asset.spec.ts.
import { test, expect, type Page } from '@playwright/test';
import { inventorySummary, routeSummary } from '../summary-mock';

const shot = (name: string) => `test-results/r9-maintenance/${name}.png`;
const now = '2026-11-19T08:00:00Z';
const asset = { id: 'LHK-CRANE-001', projectId: 'LHK', displayName: '老河口液压抓斗起重机', assetCode: 'LHK-CRANE-001', assetType: 'CRANE', assetStatus: 'DRAFT', manufacturerId: null,
  manufacturerSnapshot: '', brandSnapshot: '', modelSnapshot: 'QZ10t-31m', serialNumber: null, manufactureDate: null, installationDate: null, commissioningDate: null,
  liftingHeightM: null, applicationType: null, controlMode: null, version: 1, specialEquipmentCode: null, useRegistrationNo: null };

function points() {
  const mk = (i: number, mech: string, name: string, addr: string | null, kind: string, type: string, extra = {}) => ({
    id: 'pt' + i, pointCode: `LHK-P${String(i).padStart(3, '0')}`, name, mechanism: mech, equipmentSlot: `S-${i}`, equipmentInstallationId: 'eq' + i, equipmentCode: `LHK-CRANE-001-S-${i}`,
    typeCode: type, plcAddressRaw: addr, addressKind: kind, rawDataType: 'INT', derivedType: 'EDGE_COUNT', unitRaw: '次', unit: 'count', activeLevel: null, edge: null,
    acquisitionSource: 'PLC', confirmationStatus: 'UNCONFIRMED', quality: 'UNVERIFIED', version: 1, sourceRow: i + 2, brandModel: '施耐德 LC1G205KUEC', lifeText: '电寿命约100万次（AC-3）',
    reviewFlags: [], openIssues: 0, meters: [`LHK-P${String(i).padStart(3, '0')}.cycles`], ...extra });
  return [mk(1, '大车机构', '总接触器', 'DB505.DBX32.0', 'BIT', 'CONTACTOR'), mk(21, '小车机构', '机械左限位', 'DB505.DBX66', 'UNPARSED', 'LIMIT_SWITCH', { openIssues: 1 }),
    { ...mk(53, '自动作业', '当日作业吨数', null, 'NONE', ''), equipmentSlot: null, equipmentCode: null, typeCode: null, derivedType: 'DAILY_VALUE', unitRaw: '小时', unit: 't', brandModel: null, lifeText: null }];
}

async function mock(page: Page, role: string) {
  const session = { userId: 'u1', displayName: '验收', projects: [{ projectId: 'LHK', projectName: '老河口行车项目', role }], csrfToken: 'x'.repeat(32), expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' };
  const wo = { id: 'wo1', code: 'SIM-WO-20261119-ABC123', projectId: 'LHK', assetId: asset.id, sourceMode: 'SIMULATION', equipmentInstallationId: 'eq1', equipmentSlot: 'GANTRY-01', ruleId: 'r1', alertId: 'al1',
    cycleNo: 1, type: 'REPLACEMENT', priority: 'HIGH', status: 'OPEN', title: '【模拟】总接触器 寿命（测试）', description: '', createdBy: 'u1', createdAt: now, assignedTo: null as string | null,
    plannedAt: null, dueAt: null, startedAt: null, completedAt: null as string | null, completedBy: null as string | null, result: null as string | null, basis: null as string | null, notes: '', version: 1 };
  const state = { orders: [] as typeof wo[], alertStatus: 'OPEN', transitions: [] as unknown[], replacement: 0, assetVersion: 3, rated: null as number | null, confirmed: [] as string[], decisions: [] as unknown[] };
  const rule = (over: object) => ({ id: 'r1', ruleCode: 'T-HYBRID', version: 1, name: '总接触器 混合规则（测试）', kind: 'REPLACEMENT', type: 'HYBRID', equipmentSlot: 'GANTRY-01', meterCode: 'LHK-P001.cycles',
    intervalDays: 365, intervalMeter: 1000000, leadDays: [90, 30, 7], warnPercents: [80, 90], basis: 'SIMULATION / TEST threshold fixture', approvalStatus: 'DRAFT_UNAPPROVED', approvedBy: null,
    effectiveFrom: null, testOnly: true, sourceMode: 'SIMULATION', cycleNo: 1, status: 'DUE', nextDueAt: '2026-12-19T08:00:00Z', nextDueMeter: 1000000, progressPercent: 100, daysLeft: 30, evaluatedAt: now, ...over });
  await page.route('**/auth/session', (r) => r.fulfill({ json: session }));
  await routeSummary(page, async (c) => inventorySummary([{ id: 'LHK', adminCode: 420682 }], [{ projectId: 'LHK' }], c.region));
  await page.route('**/api/v1/**', async (r) => {
    const url = new URL(r.request().url());
    const p = url.pathname.replace('/api/v1/', '');
    const m = r.request().method();
    if (p === 'admin/me') return r.fulfill({ json: { userId: 'u1', systemRoles: [], capabilities: {}, accountServiceConfigured: false } });
    if (p === 'projects') return r.fulfill({ json: [{ id: 'LHK', name: '老河口行车项目', lifecycle: 'PLANNED', province: '湖北省', city: '襄阳市', county: '老河口市', locationLabel: '湖北省 襄阳市 老河口市', adminCode: 420682, scheduleText: '', notes: '', simulatedCount: 1, runtimeDeviceCount: 1, realConnectedCount: 0, assetDeviceCount: 1 }] });
    if (p === `assets/${asset.id}`) return r.fulfill({ json: asset });
    if (p === `assets/${asset.id}/location`) return r.fulfill({ json: { assetId: asset.id, current: null } });
    if (p === `assets/${asset.id}/points`) return r.fulfill({ json: points() });
    if (p === `assets/${asset.id}/point-issues`) return r.fulfill({ json: [
      { id: 'i1', issueCode: 'MISSING_BIT_INDEX', pointCode: 'LHK-P021', sourceRow: 23, detail: { plcAddress: 'DB505.DBX66' }, status: 'OPEN', resolvedBy: null, resolution: null },
      { id: 'i2', issueCode: 'DUPLICATE_PLC_ADDRESS_GROUP', pointCode: null, sourceRow: null, detail: { plcAddress: 'DB505.DBX10.1' }, status: 'OPEN', resolvedBy: null, resolution: null },
      { id: 'i3', issueCode: 'UNIT_CONFLICT_HOURS_VS_TONNES', pointCode: 'LHK-P053', sourceRow: 55, detail: {}, status: 'OPEN', resolvedBy: null, resolution: null }] });
    if (p === `assets/${asset.id}/pending-attributes`) return r.fulfill({ json: [
      { id: 'pa1', version: 1, subject: 'asset', attribute: 'ratedCapacityT', candidate: 10, basis: '由型号文本 QZ10t-31m 推断，待铭牌确认', status: state.rated ? 'CONFIRMED' : 'PENDING', confirmedValue: state.rated },
      { subject: 'asset', attribute: 'spanM', candidate: 31, basis: '由型号文本 QZ10t-31m 推断，待铭牌确认', status: 'PENDING' },
      { subject: 'HOIST-38', attribute: 'ropeCount', candidate: null, basis: '清单仅一行钢丝绳', status: 'PENDING' }] });
    if (p === `assets/${asset.id}/meters`) return r.fulfill({ json: [
      { id: 'm1', meterCode: 'LHK-P001.cycles', pointCode: 'LHK-P001', metric: 'cycles', kind: 'CUMULATIVE', unit: 'count', scope: 'EQUIPMENT', equipmentSlot: 'GANTRY-01', formula: 'edge-counted rising edges', status: 'PENDING_REVIEW',
        sourceMode: 'SIMULATION', runtimeDeviceId: 'SIM-LHK-CRANE-001', lastRawValue: 1000000, accumulated: 1000000, equipmentUsage: 1000000, lastSampledAt: now, lastQuality: 'GOOD', readings: 4, today: null },
      { id: 'm2', meterCode: 'LHK-P053.production_tonnes', pointCode: 'LHK-P053', metric: 'production_tonnes', kind: 'DAILY', unit: 't', scope: 'ASSET', equipmentSlot: null, formula: 'daily tonnes', status: 'UNIT_CONFLICT_PENDING',
        sourceMode: 'SIMULATION', runtimeDeviceId: 'SIM-LHK-CRANE-001', lastRawValue: 6, accumulated: 0, equipmentUsage: null, lastSampledAt: now, lastQuality: 'GOOD', readings: 4, today: 6 },
      { id: 'm3', meterCode: 'LHK-P003.hours', pointCode: 'LHK-P003', metric: 'hours', kind: 'CUMULATIVE', unit: 'h', scope: 'EQUIPMENT', equipmentSlot: 'GANTRY-03', formula: 'ON duration', status: 'PENDING_REVIEW',
        sourceMode: null, runtimeDeviceId: null, lastRawValue: null, accumulated: null, equipmentUsage: null, lastSampledAt: null, lastQuality: null, readings: 0, today: null }] });
    if (p === `assets/${asset.id}/meters/m1/readings`) return r.fulfill({ json: [{ sampledAt: now, sourceMode: 'SIMULATION', rawValue: 1000000, quality: 'GOOD', delta: 100000, accumulated: 1000000, anomaly: null, bootId: 'b', sequence: 4 }] });
    if (p === `assets/${asset.id}/maintenance-rules`) {
      const completed = state.orders.some((o) => o.status === 'COMPLETED');
      return r.fulfill({ json: [rule(completed ? { cycleNo: 2, status: 'NORMAL', progressPercent: 0, nextDueAt: '2027-11-19T08:00:00Z', daysLeft: 365 } : {}),
        rule({ id: 'r3', ruleCode: 'SIM-SCN-HYBRID-PARTIAL', name: '【模拟】小车总接触器 混合规则（计量基准未知）', status: 'OVERDUE', dataGaps: ['METER'], reason: '部分数据不足；缺少计量基准（安装时累计值 / 初始使用量）或计量数据，无法计算使用量', progressPercent: null }),
        rule({ id: 'r2', ruleCode: 'LIFE-GANTRY-03', name: '大车机构 变频器状态 设计寿命（年限，待审核）', type: 'TIME_BASED', testOnly: false, sourceMode: null, status: null, cycleNo: null, nextDueAt: null, progressPercent: null, daysLeft: null, intervalMeter: null, intervalDays: 3650, basis: '原始清单寿命：设计寿命10~15年' })] });
    }
    if (p === `assets/${asset.id}/maintenance-alerts`) return r.fulfill({ json: [{ id: 'al1', ruleId: 'r1', ruleName: '总接触器 混合规则（测试）', assetId: asset.id, sourceMode: 'SIMULATION', cycleNo: 1, stage: 'PCT_100', triggeredAt: now, status: state.alertStatus, workOrderId: state.orders[0]?.id ?? null, detail: {} }] });
    if (p === `assets/${asset.id}/work-orders`) return r.fulfill({ json: state.orders });
    if (p === `assets/${asset.id}/maintenance-history`) return r.fulfill({ json: [
      ...(state.replacement ? [{ at: now, category: 'REPLACEMENT', sourceMode: 'SIMULATION', actor: 'u1', summary: 'GANTRY-01：寿命到期（模拟）', reference: 'wo1' }] : []),
      { at: now, category: 'LEDGER', sourceMode: 'REAL', actor: 'host:ops', summary: 'asset imported', reference: asset.id }] });
    if (p === 'maintenance-alerts/al1/work-order' && m === 'POST') {
      if (!['maintainer', 'platform_admin'].includes(role)) return r.fulfill({ status: 403, json: { error: 'Forbidden' } });
      state.orders = [{ ...wo }];
      state.alertStatus = 'WORK_ORDER';
      return r.fulfill({ status: 201, json: state.orders[0] });
    }
    if (p === 'work-orders/wo1/transitions' && m === 'POST') {
      const body = r.request().postDataJSON() as { to: string; assignedTo?: string; result?: string; basis?: string; replacement?: unknown };
      const o = state.orders[0];
      if (r.request().headers()['if-match'] !== `"${o.version}"`) return r.fulfill({ status: 409, json: { error: 'stale' } });
      state.transitions.push(body);
      Object.assign(o, { status: body.to, version: o.version + 1 }, body.to === 'ASSIGNED' ? { assignedTo: body.assignedTo } : {},
        body.to === 'COMPLETED' ? { completedAt: now, completedBy: 'u1', result: body.result, basis: body.basis } : {});
      if (body.replacement) state.replacement++;
      return r.fulfill({ json: o });
    }
    if (p === `assets/${asset.id}/readiness`) return r.fulfill({ json: {
      assetVersion: state.assetVersion, ratedCapacityT: state.rated, spanM: null, useRegistrationNo: null, latestInspectionDate: null, inspectionValidUntil: null,
      pointsTotal: 55, pointsConfirmed: state.confirmed.length, issuesOpen: 82, pendingOpen: state.rated ? 2 : 3, slotsTotal: 51, slotsWithInstalledAt: 0,
      rulesApproved: 0, rulesDraft: 53, realRuntimeRegistered: false, realBaselines: 0,
      slots: [{ equipmentSlot: 'GANTRY-01', equipmentInstallationId: 'eq1', equipmentCode: 'LHK-CRANE-001-GANTRY-01', typeCode: 'CONTACTOR', installedAt: null, version: 1 }],
      baselines: [{ equipmentSlot: 'GANTRY-01', meterCode: 'LHK-P001.cycles', sourceMode: 'SIMULATION', initialUsage: 960, baselineKind: 'INITIAL_RECORD', recordedBy: 'host:ops', recordedAt: now }],
      blockers: ['未登记真实运行身份（真实硬件未接入）', '点位待现场确认 55 / 55', '维护规则均未审批（草案 53 条，未生效）'] } });
    if (p === `assets/${asset.id}/point-changes`) return r.fulfill({ json: [] });
    if (p.startsWith(`assets/${asset.id}/pending-attributes/`) && m === 'POST') {
      if (role !== 'platform_admin') return r.fulfill({ status: 403, json: { error: 'Forbidden' } });
      if (r.request().headers()['if-match'] !== '"1"') return r.fulfill({ status: 409, json: { error: 'stale' } });
      state.decisions.push(r.request().postDataJSON());
      state.rated = 10;
      return r.fulfill({ status: 204, body: '' });
    }
    if (p === 'maintenance/center') return r.fulfill({ json: {
      rows: [
        { projectId: 'LHK', projectName: '老河口行车项目', assetId: asset.id, assetName: asset.displayName, assetCode: asset.assetCode, mechanism: '大车机构', equipmentSlot: 'GANTRY-01', equipmentType: 'CONTACTOR', category: 'REPLACEMENT_DUE', ruleId: 'r1', ruleName: '总接触器 混合规则（测试）', approvalStatus: 'DRAFT_UNAPPROVED', sourceMode: 'SIMULATION', status: 'DUE', nextDueAt: null, progressPercent: 100, daysLeft: 30, openWorkOrders: state.orders.filter((o) => o.status !== 'COMPLETED').length },
        { projectId: 'LHK', projectName: '老河口行车项目', assetId: asset.id, assetName: asset.displayName, assetCode: asset.assetCode, mechanism: '大车机构', equipmentSlot: 'GANTRY-03', equipmentType: 'VFD', category: 'REPLACEMENT_DUE', ruleId: 'r2', ruleName: '变频器 设计寿命（待审核）', approvalStatus: 'DRAFT_UNAPPROVED', sourceMode: 'REAL', status: 'DRAFT', nextDueAt: null, progressPercent: null, daysLeft: null, openWorkOrders: 0 },
        { projectId: 'LHK', projectName: '老河口行车项目', assetId: asset.id, assetName: asset.displayName, assetCode: asset.assetCode, mechanism: null, equipmentSlot: null, equipmentType: null, category: 'STATUTORY_INSPECTION', ruleId: null, ruleName: '法定定期检验', approvalStatus: 'SOURCE', sourceMode: 'REAL', status: 'INSUFFICIENT_DATA', nextDueAt: null, progressPercent: null, daysLeft: null, openWorkOrders: 0 }],
      alerts: [{ id: 'al1', ruleId: 'r1', ruleName: '总接触器 混合规则（测试）', assetId: asset.id, sourceMode: 'SIMULATION', cycleNo: 1, stage: 'PCT_100', triggeredAt: now, status: state.alertStatus, workOrderId: null, detail: {} }],
      workOrders: state.orders,
      attention: { maintenanceDue: 0, maintenanceDueSoon: 0, inspectionDue: 0, inspectionUnknown: 1, openWorkOrders: 0, insufficientData: 0, draftRules: 41, simulationDue: 1, simulationWorkOrders: state.orders.length },
      generatedAt: now } });
    return r.fulfill({ json: [] });
  });
  return state;
}

test('首台资产详情：台账与点位（原文地址/待确认/核查项）、运行监测（模拟醒目标识）、寿命与维护 → 工单 → 更换 → 下一周期、历史', async ({ page }) => {
  const state = await mock(page, 'maintainer');
  await page.goto(`/#/assets/${asset.id}`);
  const d = page.getByTestId('asset-detail');
  await d.getByTestId('tab-ledger').click();
  await expect(d.getByTestId('r9-pending')).toContainText('额定起重量（t）');
  await expect(d.getByTestId('r9-pending')).toContainText('候选 10 · 待确认');
  await expect(d.locator('[data-point="LHK-P001"]')).toContainText('DB505.DBX32.0');
  await expect(d.locator('[data-point="LHK-P021"]')).toContainText('地址待核（无法解析）');
  await expect(d.locator('[data-point="LHK-P053"]')).toContainText('作业统计（非设备）');
  await expect(d.getByTestId('r9-issues')).toContainText('地址缺少位索引');
  await expect(d.getByTestId('r9-issues')).toContainText('吨数单位写为「小时」');
  await page.screenshot({ path: shot('1-ledger'), fullPage: true });

  await d.getByTestId('tab-monitor').click();
  await expect(d.getByTestId('r9-sim-banner')).toContainText('模拟数据');
  await expect(d.getByTestId('r9-no-real')).toBeVisible();
  await expect(d.locator('[data-meter="LHK-P001.cycles"] [data-mode-badge="SIMULATION"]')).toHaveText('模拟数据');
  await expect(d.locator('[data-meter="LHK-P053.production_tonnes"]')).toContainText('单位冲突待确认');
  await expect(d.locator('[data-meter="LHK-P003.hours"]')).toContainText('无数据');
  await d.locator('[data-meter="LHK-P001.cycles"]').click();
  await expect(d.getByTestId('r9-readings')).toContainText('1,000,000');
  await page.screenshot({ path: shot('2-monitor'), fullPage: true });

  await d.getByTestId('tab-maintenance').click();
  await expect(d.getByTestId('r9-rules')).toContainText('模拟测试规则');
  await expect(d.locator('[data-rule="T-HYBRID"]')).toContainText('到期');
  await d.getByLabel('规则筛选').selectOption('DRAFT');
  await expect(d.locator('[data-rule="LIFE-GANTRY-03"]')).toContainText('待审核（未生效）');
  await d.getByLabel('规则筛选').selectOption('ACTIVE');
  await d.getByTestId('r9-create-wo').click();
  await expect(d.getByTestId('r9-notice')).toContainText('已生成工单 SIM-WO');
  await d.getByTestId('r9-wo-open').click();
  const dlg = page.getByTestId('r9-wo-dialog');
  await expect(dlg).toContainText('模拟数据');
  await dlg.getByTestId('r9-wo-assignee').fill('维修班 张工');
  await dlg.getByTestId('r9-wo-assign').click();
  await dlg.getByTestId('r9-wo-start').click();
  await expect(dlg.getByTestId('r9-wo-complete')).toBeDisabled();
  await dlg.getByTestId('r9-wo-basis').fill('更换总接触器（模拟）');
  await dlg.getByTestId('r9-wo-replace').check();
  await expect(dlg.getByTestId('r9-wo-complete')).toBeDisabled();
  await dlg.getByTestId('r9-wo-reason').fill('达到寿命阈值（模拟）');
  await page.screenshot({ path: shot('3-work-order') });
  await dlg.getByTestId('r9-wo-complete').click();
  await expect(d.getByTestId('r9-notice')).toContainText('已完成');
  expect(state.replacement).toBe(1);
  await expect(d.locator('[data-rule="T-HYBRID"]')).toContainText('第 2 周期');
  await expect(d.locator('[data-rule="T-HYBRID"]')).toContainText('正常');
  await expect(d.locator('[data-wo-status="COMPLETED"]')).toBeVisible();

  await d.getByTestId('tab-history').click();
  await expect(d.locator('[data-category="REPLACEMENT"]')).toContainText('模拟数据');
  await expect(d.locator('[data-category="LEDGER"]')).toBeVisible();
  await page.screenshot({ path: shot('4-history'), fullPage: true });
});

test('只读角色：可查看全部标签，没有生成工单 / 处理 / 核查入口', async ({ page }) => {
  await mock(page, 'viewer');
  await page.goto(`/#/assets/${asset.id}`);
  const d = page.getByTestId('asset-detail');
  await d.getByTestId('tab-maintenance').click();
  await expect(d.getByTestId('r9-alerts')).toContainText('使用量 100%');
  await expect(d.getByTestId('r9-create-wo')).toHaveCount(0);
  await d.getByTestId('tab-ledger').click();
  await expect(d.getByTestId('r9-issues')).toBeVisible();
  await expect(d.getByRole('button', { name: '记录处理结果' })).toHaveCount(0);
});

test('维护与到期中心：默认仅真实，切换显示模拟；按机构 / 器件 / 状态筛选；首页关注接入维护与检验', async ({ page }) => {
  await mock(page, 'maintainer');
  await page.goto('/#/maintenance');
  const c = page.getByTestId('maintenance-center');
  await expect(c.getByTestId('mc-kpis')).toContainText('待审核规则');
  await expect(c.locator('[data-kpi="sim"]')).toContainText('不计入真实');
  await expect(c.locator('[data-mode="SIMULATION"]')).toHaveCount(0);
  await expect(c.getByTestId('mc-rows')).toContainText('法定定期检验');
  await expect(c.getByTestId('mc-rows')).toContainText('数据不足');
  await c.getByTestId('mc-mode').selectOption('ALL');
  await expect(c.locator('[data-mode="SIMULATION"]')).toHaveCount(1);
  await c.getByLabel('器件').selectOption('GANTRY-01');
  await expect(c.locator('tbody tr')).toHaveCount(1);
  await c.getByLabel('器件').selectOption('');
  await c.getByLabel('状态').selectOption('INSUFFICIENT_DATA');
  await expect(c.locator('tbody tr')).toHaveCount(1);
  await page.screenshot({ path: shot('5-center'), fullPage: true });
  await page.goto('/#/overview?view=2d');
  const att = page.getByTestId('home-attention');
  await expect(att.locator('[data-attention="work-orders"]')).toContainText('0');
  await expect(att.locator('[data-attention="inspection"]')).toContainText('未登记 1');
  await expect(att.locator('[data-attention="maintenance-due"]')).toBeVisible();
  await att.locator('[data-attention="work-orders"]').click();
  await expect(page.getByTestId('maintenance-center')).toBeVisible();
});

for (const [w, h] of [[375, 812], [1440, 900]] as const) {
  test(`${w}×${h}：资产维护标签与维护中心无横向溢出`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: h });
    await mock(page, 'maintainer');
    const overflow = () => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    await page.goto(`/#/assets/${asset.id}`);
    for (const tab of ['tab-ledger', 'tab-monitor', 'tab-maintenance', 'tab-history', 'tab-readiness']) {
      await page.getByTestId(tab).click();
      await page.waitForTimeout(200);
      expect(await overflow(), tab).toBeLessThanOrEqual(0);
    }
    await page.screenshot({ path: shot(`6-maintenance-${w}`), fullPage: true });
    await page.goto('/#/maintenance');
    await expect(page.getByTestId('mc-rows')).toBeVisible();
    const offenders = await page.evaluate(() => {
      const vw = document.documentElement.clientWidth;
      return [...document.querySelectorAll<HTMLElement>('body *')].filter((el) => el.getBoundingClientRect().right > vw + 0.5 && el.getBoundingClientRect().width > 0)
        .slice(0, 6).map((el) => `${el.tagName.toLowerCase()}.${[...el.classList].join('.')}@${Math.round(el.getBoundingClientRect().right)}`);
    });
    expect(await overflow(), offenders.join(' ')).toBeLessThanOrEqual(0);
    await page.screenshot({ path: shot(`7-center-${w}`), fullPage: true });
  });
}

test('接入准备：阻断项、铭牌确认（平台管理员 + If-Match + 依据）；只读角色无入口；混合规则部分数据不足', async ({ page }) => {
  const state = await mock(page, 'platform_admin');
  await page.goto(`/#/assets/${asset.id}`);
  const d = page.getByTestId('asset-detail');
  await d.getByTestId('tab-readiness').click();
  await expect(d.getByTestId('r9-ready-banner')).toContainText('未经确认的点位只能用于模拟验收');
  await expect(d.getByTestId('r9-ready-blockers')).toContainText('真实硬件未接入');
  await expect(d.getByTestId('r9-ready-blockers')).toContainText('草案 53 条');
  await expect(d.locator('[data-kpi="points"]')).toContainText('0 / 55');
  await d.getByTestId('r9-ready-nameplate').locator('[data-attr="ratedCapacityT"]').getByTestId('r9-pending-confirm').click();
  const dlg = page.getByTestId('r9-ready-dialog');
  await expect(dlg.getByTestId('r9-ready-save')).toBeDisabled();
  await dlg.getByTestId('r9-ready-basis').fill('铭牌照片 IMG-001');
  await dlg.getByTestId('r9-ready-save').click();
  await expect(d.getByTestId('r9-ready-notice')).toContainText('已确认');
  expect(state.decisions).toEqual([{ status: 'CONFIRMED', value: 10, basis: '铭牌照片 IMG-001' }]);
  await expect(d.getByTestId('r9-ready-nameplate').locator('[data-attr="ratedCapacityT"]')).toContainText('已确认 10');
  await page.screenshot({ path: shot('8-readiness'), fullPage: true });
  await d.getByTestId('tab-maintenance').click();
  await d.getByLabel('规则筛选').selectOption('ALL');
  const partial = d.locator('[data-rule="SIM-SCN-HYBRID-PARTIAL"]');
  await expect(partial).toContainText('逾期（部分数据不足）');
  await expect(partial.getByTestId('r9-rule-reason')).toContainText('初始使用量');
});

test('接入准备：维护人员只读（无确认 / 审批入口）', async ({ page }) => {
  await mock(page, 'maintainer');
  await page.goto(`/#/assets/${asset.id}`);
  const d = page.getByTestId('asset-detail');
  await d.getByTestId('tab-readiness').click();
  await expect(d.getByTestId('r9-ready-summary')).toBeVisible();
  await expect(d.getByTestId('r9-pending-confirm')).toHaveCount(0);
  await expect(d.getByTestId('r9-inspection-open')).toHaveCount(0);
  await expect(d.getByTestId('r9-baseline-open')).toHaveCount(0);
});
