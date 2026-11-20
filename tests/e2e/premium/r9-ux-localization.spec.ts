// R9-UX — visible-text localization acceptance. The detector is checked against counterexamples
// first (so a detector that accepts everything fails here), then applied to the production routes
// with realistic mocked data. Route-specific business flows keep their own specs; those specs also
// call expectChineseUI on the screens they reach (r9-maintenance, p1-personnel, …).
import { test, expect, type Page } from '@playwright/test';
import { findLeaks, expectChineseUI } from './zh-guard';
import { inventorySummary, routeSummary } from '../summary-mock';

test('检测器反例：枚举泄漏 / 英文公式 / 双语装饰 / 未翻译错误都会被识别，合法编号与缩写不误报', () => {
  // counterexamples — every one must be caught
  const bad: [string, string][] = [
    ['DRAFT_UNAPPROVED', 'RAW_ENUM'], ['状态：PCT_100', 'RAW_ENUM'], ['器件类型 CONTACTOR', 'RAW_ENUM'], ['角色 platform_admin', 'RAW_ENUM'],
    ['单位 count', 'RAW_ENUM'], ['EDGE_COUNT · INT', 'RAW_ENUM'],
    ['edge-counted rising edges', 'ENGLISH_FORMULA'], ['ON duration', 'ENGLISH_FORMULA'], ['daily tonnes', 'ENGLISH_FORMULA'],
    ['EQUIPMENT CATALOG', 'BILINGUAL'], ['DEVICE CONTEXT', 'BILINGUAL'], ['ASSET CENTER / 资产中心', 'BILINGUAL'], ['资产中心 / Asset Center', 'BILINGUAL'],
    ['Forbidden', 'UNTRANSLATED_ERROR'], ['加载失败：Failed to fetch', 'UNTRANSLATED_ERROR'], ['name is required', 'UNTRANSLATED_ERROR'],
    ['保存失败：stale', 'UNTRANSLATED_ERROR'], ['[object Object]', 'UNTRANSLATED_ERROR'], ['Internal Server Error', 'UNTRANSLATED_ERROR'],
  ];
  for (const [text, kind] of bad) expect(findLeaks([text]).map((l) => l.kind), text).toContain(kind);
  // allowed — must not be flagged
  const ok = [
    'LHK-CRANE-001', 'SIM-LHK-CRANE-001', 'SIM-WO-20261119-ABC123', 'LHK-P001', 'DB505.DBX32.0', '施耐德 LC1G205KUEC', 'QZ10t-31m',
    'GB/T 3811-2008', 'PLC 地址', 'RFID 读写器', 'MQTT 接入', '整数（INT）', '浮点（FLOAT）', '旧类型（编码 CRANE，保持不变）', 'U·DAO', '友道智造 · 行车综合管理平台',
    '使用量 100%', '1,000,000 次', '103.017 小时', 'WGS84 坐标', 'v1', '基于 v1', '第 2 周期', '候选口径（待现场确认）：按上升沿计次',
  ];
  for (const text of ok) expect(findLeaks([text]), text).toEqual([]);
});

// ------------------------------------------------------------------------------------------------
// Route sweep with a realistic mock (project manager + system admin so every production route renders).
// ------------------------------------------------------------------------------------------------
const now = '2026-11-20T08:00:00Z';
const project = { id: 'LHK', name: '老河口行车项目', lifecycle: 'PLANNED', province: '湖北省', city: '襄阳市', county: '老河口市', locationLabel: '湖北省 襄阳市 老河口市', adminCode: 420682,
  scheduleText: '', notes: '', simulatedCount: 1, runtimeDeviceCount: 1, realConnectedCount: 0, assetDeviceCount: 1 };
const asset = { id: 'LHK-CRANE-001', projectId: 'LHK', displayName: '老河口液压抓斗起重机', assetCode: 'LHK-CRANE-001', assetType: 'CRANE', assetStatus: 'DRAFT', manufacturerId: null,
  manufacturerSnapshot: '', brandSnapshot: '', modelSnapshot: 'QZ10t-31m', serialNumber: null, manufactureDate: null, installationDate: null, commissioningDate: null,
  liftingHeightM: null, applicationType: null, controlMode: null, version: 1, specialEquipmentCode: null, useRegistrationNo: null };

async function mockAll(page: Page, opts: { fail?: number } = {}) {
  const session = { userId: 'u1', displayName: '验收', projects: [{ projectId: 'LHK', projectName: '老河口行车项目', role: 'platform_admin' }], csrfToken: 'x'.repeat(32),
    expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' };
  await page.route('**/auth/session', (r) => r.fulfill({ json: session }));
  await routeSummary(page, async (c) => inventorySummary([{ id: 'LHK', adminCode: 420682 }], [{ projectId: 'LHK' }], c.region));
  await page.route('**/api/v1/**', async (r) => {
    const p = new URL(r.request().url()).pathname.replace('/api/v1/', '');
    if (opts.fail && p !== 'admin/me') {
      const body = { 403: { error: 'Forbidden', code: 'FORBIDDEN' }, 404: { error: 'Not Found', code: 'NOT_FOUND' }, 500: { error: 'Internal Server Error' }, 503: { error: 'Service Unavailable' } }[opts.fail];
      return r.fulfill({ status: opts.fail, json: body ?? { error: 'Bad Request' } });
    }
    if (p === 'admin/me') return r.fulfill({ json: { userId: 'u1', systemRoles: ['system_admin'], capabilities: { managePeople: true }, accountServiceConfigured: false } });
    if (p === 'projects') return r.fulfill({ json: [project] });
    if (p === 'assets') return r.fulfill({ json: [asset] });
    if (p === `assets/${asset.id}`) return r.fulfill({ json: asset });
    if (p === `assets/${asset.id}/location`) return r.fulfill({ json: { assetId: asset.id, current: null } });
    if (p === 'devices') return r.fulfill({ json: [] });
    if (p === 'alarms') return r.fulfill({ json: [] });
    if (p === 'maintenance/center') return r.fulfill({ json: { rows: [], alerts: [], workOrders: [],
      attention: { maintenanceDue: 0, maintenanceDueSoon: 0, inspectionDue: 0, inspectionUnknown: 1, openWorkOrders: 0, insufficientData: 0, draftRules: 53, simulationDue: 0, simulationWorkOrders: 0 }, generatedAt: now } });
    return r.fulfill({ json: [] });
  });
}

const ROUTES: [string, string][] = [
  ['/#/overview?view=2d', '运营总览'], ['/#/runtime', '运行状态'], ['/#/projects', '项目'], ['/#/devices', '设备'], ['/#/alarms', '告警'],
  ['/#/assets', '资产中心'], [`/#/assets/${asset.id}`, '资产详情'], ['/#/locations', '位置'], ['/#/catalog', '目录'], ['/#/maintenance', '维护与到期'],
  ['/#/portfolio', '项目组合'], ['/#/system/people', '人员'],
];

for (const [w, h] of [[1280, 800], [1440, 900], [1920, 1080], [375, 812], [390, 844]] as const) {
  test(`${w}×${h}：全部生产路由可见文本中文化、无页面级横向溢出`, async ({ page }) => {
    test.setTimeout(90_000);
    await page.setViewportSize({ width: w, height: h });
    await mockAll(page);
    for (const [url, name] of ROUTES) {
      await page.goto('about:blank');
      await page.goto(url);
      await page.waitForLoadState('networkidle');
      await page.waitForTimeout(150);
      await expectChineseUI(page, `${name} ${url} @${w}`);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      expect(overflow, `${name} @${w} 横向溢出`).toBeLessThanOrEqual(0);
    }
  });
}

const EXPECTED_ERROR: Record<number, RegExp> = { 403: /权限|无权/, 404: /不存在|已删除|未找到/, 500: /服务暂时?不可用|读取失败|加载失败|稍后重试/, 503: /服务暂时?不可用|读取失败|加载失败|稍后重试/ };
for (const status of [403, 404, 500, 503]) {
  test(`接口 ${status}：错误提示为中文，不显示英文诊断作为主信息`, async ({ page }) => {
    await mockAll(page, { fail: status });
    for (const url of ['/#/assets', `/#/assets/${asset.id}`, '/#/maintenance', '/#/catalog', '/#/locations']) {
      await page.goto('about:blank');
      await page.goto(url);
      await page.waitForLoadState('networkidle');
      await page.waitForTimeout(150);
      if (url === '/#/locations') await page.getByLabel('选择项目').first().selectOption('LHK');
      await expectChineseUI(page, `${url} HTTP ${status}`);
      // positive: a Chinese failure message is actually shown (not an empty page)
      await expect(page.locator('main, [role=alert], .error, .state-error').filter({ hasText: EXPECTED_ERROR[status] }).first(), `${url} HTTP ${status} 中文错误提示`).toBeVisible({ timeout: 20_000 });
    }
  });
}

test('断网：读取失败提示为中文网络异常', async ({ page }) => {
  await mockAll(page);
  await page.route(/\/api\/v1\/assets(\?|$)/, (r) => r.abort('internetdisconnected'));
  await page.goto('/#/assets');
  await page.waitForLoadState('networkidle');
  await page.waitForTimeout(300);
  await expectChineseUI(page, '资产中心 断网');
  await expect(page.getByText(/网络连接异常|读取失败/).first()).toBeVisible({ timeout: 20_000 });
});
