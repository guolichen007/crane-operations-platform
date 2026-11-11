// Q3 formal project → Inventory → projects list → national map → asset — real browser against a
// stateful mocked API that follows the server rules (only granted platform_admin may register;
// first registration only; names derived from the code; no runtime → 0 REAL/SIM; summary from the
// same Inventory). The real BFF + PostgreSQL proof is inventory_registration_http_test.go and the
// real Keycloak proof tests/e2e/b4/portfolio-grants.spec.ts (inventory step).
import { test, expect, type Page } from '@playwright/test';
import { inventorySummary, routeSummary } from '../summary-mock';

const shot = (name: string) => `test-results/q3-inventory/${name}.png`;
type Grant = { projectId: string; projectName: string; role: string };
const NAMES: Record<number, [string, string, string]> = { 330481: ['浙江省', '嘉兴市', '海宁市'], 330400: ['浙江省', '嘉兴市', ''], 440300: ['广东省', '深圳市', ''] };

async function mock(page: Page, opts: { grants?: Grant[]; failPending?: boolean; failRegister?: number; failGeo?: boolean; registered?: boolean; abortAfterCommit?: boolean; revokeDuring?: boolean; registeredElsewhere?: number; failWith?: { status: number; error: string } | 'timeout'; failProjects?: boolean } = {}) {
  const grants = opts.grants ?? [{ projectId: 'FP-1', projectName: 'Q3 华东新装项目', role: 'platform_admin' }];
  const state = { registered: opts.registered ?? false, adminCode: 330481, posts: 0, granted: true };
  const assets = [
    { assetDeviceId: 'AS-1', projectId: 'FP-1', displayName: 'Q3 1 号行车', craneModel: null, ratedCapacityT: null, spanM: null, workClass: null, grabType: null, lifecycle: 'PLANNED', runtimeSource: null, runtimeDeviceId: null, connectivity: null },
    { assetDeviceId: 'AS-2', projectId: 'FP-1', displayName: 'Q3 2 号行车', craneModel: null, ratedCapacityT: null, spanM: null, workClass: null, grabType: null, lifecycle: 'PLANNED', runtimeSource: null, runtimeDeviceId: null, connectivity: null },
  ];
  state.granted = grants.some((g) => g.projectId === 'FP-1');
  const inventoryProject = () => {
    const [province, city, county] = NAMES[state.adminCode] ?? ['', '', ''];
    return { id: 'FP-1', name: 'Q3 华东新装项目', lifecycle: 'PLANNED', province, city, county, locationLabel: [province, city, county].filter(Boolean).join(' '), adminCode: state.adminCode, scheduleText: '2027 Q1 安装', notes: '', assetDeviceCount: 2, runtimeDeviceCount: 0, realConnectedCount: 0, simulatedCount: 0 };
  };
  if (opts.failGeo) await page.route('**/geo/geo-manifest.json', (r) => r.fulfill({ status: 500, body: '' }));
  await page.route('**/auth/session', (r) => r.fulfill({ json: { userId: 'u-q3', displayName: 'q3', projects: grants, csrfToken: 'x'.repeat(32), expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' } }));
  await page.route('**/api/v1/**', async (r) => {
    const p = new URL(r.request().url()).pathname.replace('/api/v1/', '');
    const m = r.request().method();
    const granted = state.granted;
    const visible = granted && state.registered;
    if (p === 'inventory/pending-projects') {
      if (opts.failPending) return r.fulfill({ status: 503, json: { error: 'database unavailable' } });
      return r.fulfill({ json: granted && !state.registered ? [{ id: 'FP-1', name: 'Q3 华东新装项目', portfolioScopeId: 'PS-A', projectPhase: 'REGISTERED', role: grants[0].role, canRegister: grants[0].role === 'platform_admin', assetCount: 2 }] : [] });
    }
    if (p === 'projects/FP-1/inventory-registration' && m === 'POST') {
      state.posts++;
      if (opts.revokeDuring) {
        // The grant is revoked while the request is in flight: refused, and the project is no
        // longer readable nor pending for this user.
        state.granted = false;
        return r.fulfill({ status: 403, json: { error: 'Forbidden' } });
      }
      if (opts.registeredElsewhere !== undefined) {
        state.registered = true;
        state.adminCode = opts.registeredElsewhere;
        return r.fulfill({ status: 409, json: { error: 'project is already registered in Inventory' } });
      }
      if (opts.failWith === 'timeout') return r.abort('timedout');
      if (opts.failWith) return r.fulfill({ status: opts.failWith.status, json: { error: opts.failWith.error } });
      if (opts.failRegister && state.posts <= opts.failRegister) return r.fulfill({ status: 503, json: { error: 'administrative index unavailable' } });
      if (state.registered) return r.fulfill({ status: 409, json: { error: 'project is already registered in Inventory' } });
      const b = r.request().postDataJSON() as { adminCode: number };
      state.registered = true;
      state.adminCode = b.adminCode;
      // Committed on the server, but the response never reaches the browser.
      if (opts.abortAfterCommit) return r.abort('connectionreset');
      return r.fulfill({ json: inventoryProject() });
    }
    if (p === 'projects') {
      if (opts.failProjects) return r.fulfill({ status: 503, json: { error: 'inventory unavailable' } });
      return r.fulfill({ json: visible ? [inventoryProject()] : [] });
    }
    if (p === 'assets/devices') return r.fulfill({ json: visible ? assets : [] });
    if (p === 'assets/AS-1') return r.fulfill({ json: { id: 'AS-1', projectId: 'FP-1', displayName: 'Q3 1 号行车', assetCode: 'Q3-CRN-1', assetType: 'CRANE', assetStatus: 'ACTIVE', manufacturerId: null, manufacturerSnapshot: '', brandSnapshot: '', modelSnapshot: '', serialNumber: null, manufactureDate: null, installationDate: null, commissioningDate: null, liftingHeightM: null, applicationType: null, controlMode: null, version: 1, specialEquipmentCode: null, useRegistrationNo: null } });
    if (p === 'assets') return r.fulfill({ json: granted ? [{ id: 'AS-1', projectId: 'FP-1', displayName: 'Q3 1 号行车', assetCode: 'Q3-CRN-1', assetType: 'CRANE', assetStatus: 'ACTIVE', manufacturerId: null, manufacturerSnapshot: '', brandSnapshot: '', modelSnapshot: '', serialNumber: null, manufactureDate: null, installationDate: null, commissioningDate: null, liftingHeightM: null, applicationType: null, controlMode: null, version: 1, specialEquipmentCode: null, useRegistrationNo: null }] : [] });
    return r.fulfill({ json: [] });
  });
  await routeSummary(page, (c) => inventorySummary(state.registered && state.granted ? [{ id: 'FP-1', adminCode: state.adminCode }] : [], state.registered && state.granted ? [{ projectId: 'FP-1' }, { projectId: 'FP-1' }] : [], c.region));
  return state;
}

async function register(page: Page, codes: { province: string; prefecture?: string; county?: string }) {
  await page.getByTestId('register-open').click();
  const form = page.getByTestId('registration-form');
  await form.getByLabel('项目生命周期').selectOption('PLANNED');
  await form.getByLabel('省', { exact: true }).selectOption(codes.province);
  if (codes.prefecture) await form.getByLabel('市', { exact: true }).selectOption(codes.prefecture);
  if (codes.county) await form.getByLabel('区县').selectOption(codes.county);
  return form;
}

test('项目管理员：登记运营信息 → 项目列表同一 ID → 首页按行政区显示 → 设备面板 → 资产台账', async ({ page }) => {
  const state = await mock(page);
  await page.goto('/#/projects');
  await expect(page.getByTestId('pending-table')).toContainText('FP-1');
  await expect(page.locator('[data-testid=inventory-project]')).toHaveCount(0);
  const form = await register(page, { province: '330000', prefecture: '330400', county: '330481' });
  await expect(form.getByTestId('precision')).toContainText('浙江省 嘉兴市 海宁市（区县级行政区定位，非设备精确坐标）');
  await form.getByLabel('实施计划备注').fill('2027 Q1 安装');
  await page.screenshot({ path: shot('1-registration-form') });
  await form.getByTestId('registration-submit').click();
  await expect(form).toBeHidden();
  expect(state.posts).toBe(1);
  await expect(page.getByTestId('registered-notice')).toContainText('FP-1');
  const card = page.locator('[data-project-id="FP-1"]');
  await expect(card).toContainText('Q3 华东新装项目');
  await expect(card).toContainText('浙江省 嘉兴市 海宁市 · 行政区级定位');
  await expect(card.getByTestId('runtime-source')).toHaveText('尚未接入运行源');
  await expect(card).not.toContainText('真实在线');
  await expect(card).toContainText('行车资产 2');
  await page.screenshot({ path: shot('2-projects-list') });

  // Homepage (2D) at the county: same 2 assets, REAL online 0, no runtime 2.
  await card.getByRole('link', { name: /沙盘定位/ }).click();
  await expect(page).toHaveURL(/overview\?project=FP-1/);
  await page.goto('/#/overview?view=2d&region=330481');
  await expect(page.getByTestId('home-kpis')).toHaveAttribute('data-state', 'ready');
  await expect(page.locator('[data-kpi=registered] .home-kpi-value')).toHaveText('2台');
  await expect(page.locator('[data-kpi=real-connected] .home-kpi-value')).toHaveText('0台');
  await expect(page.locator('[data-kpi=no-runtime] .home-kpi-value')).toHaveText('2台');
  await page.screenshot({ path: shot('3-homepage-county') });
  await page.goto('/#/overview?view=2d&project=FP-1');
  await expect(page.getByTestId('project-panel')).toHaveAttribute('data-project-id', 'FP-1');
  await page.getByTestId('project-panel').locator('[data-device-id]').first().click();
  const panel = page.getByTestId('device-panel');
  await expect(panel).toContainText('未接入');
  await expect(panel).toContainText('行政区中心示意，非设备精确坐标');
  await page.screenshot({ path: shot('4-device-panel') });
  await panel.getByTestId('asset-ledger-link').click();
  await expect(page).toHaveURL(/#\/assets\/AS-1/);
  await expect(page.getByTestId('asset-detail')).toContainText('Q3-CRN-1');
});

test('仅有查看权限：可见待登记项目但不能登记；无授权账号看不到该项目', async ({ page }) => {
  await mock(page, { grants: [{ projectId: 'FP-1', projectName: 'Q3', role: 'viewer' }] });
  await page.goto('/#/projects');
  await expect(page.getByTestId('pending-table')).toContainText('需项目管理员登记');
  await expect(page.getByTestId('register-open')).toHaveCount(0);

  const other = await page.context().newPage();
  await mock(other, { grants: [{ projectId: 'P-OTHER', projectName: '别的项目', role: 'platform_admin' }], registered: true });
  await other.goto('/#/projects');
  await expect(other.getByTestId('pending-table')).toHaveCount(0);
  await expect(other.locator('[data-project-id="FP-1"]')).toHaveCount(0);
});

test('失败不显示假数据：待登记 503、行政区清单失败、登记 503 后保留输入并可重试', async ({ page }) => {
  await mock(page, { failPending: true });
  await page.goto('/#/projects');
  await expect(page.getByTestId('pending-error')).toContainText('服务暂不可用');

  const p2 = await page.context().newPage();
  await mock(p2, { failGeo: true });
  await p2.goto('/#/projects');
  await p2.getByTestId('register-open').click();
  await expect(p2.getByTestId('geo-error')).toBeVisible();
  await expect(p2.getByTestId('registration-form').getByLabel('省', { exact: true })).toBeDisabled();
  await p2.getByTestId('registration-submit').click();
  await expect(p2.getByTestId('registration-form').locator('.field-error').first()).toBeVisible();

  const p3 = await page.context().newPage();
  const s3 = await mock(p3, { failRegister: 1 });
  await p3.goto('/#/projects');
  const form = await register(p3, { province: '440000', prefecture: '440300' });
  await form.getByTestId('registration-submit').click();
  await expect(form.getByTestId('registration-error')).toContainText('行政区数据暂不可用');
  await expect(form.getByLabel('市', { exact: true })).toHaveValue('440300');
  await p3.screenshot({ path: shot('5-register-503') });
  await form.getByTestId('registration-submit').click();
  await expect(form).toBeHidden();
  expect(s3.posts).toBe(2);
  await expect(p3.locator('[data-project-id="FP-1"]')).toContainText('广东省 深圳市');
});

test('Q3-R1：登记已提交但响应丢失时，向服务端确认状态并关闭表单，不重复提交', async ({ page }) => {
  const state = await mock(page, { abortAfterCommit: true });
  await page.goto('/#/projects');
  const form = await register(page, { province: '330000', prefecture: '330400', county: '330481' });
  await form.getByTestId('registration-submit').click();
  await expect(page.getByTestId('registration-confirmed')).toContainText('已登记（服务端确认）');
  await expect(form).toBeHidden();
  expect(state.posts).toBe(1);
  await expect(page.locator('[data-project-id="FP-1"]')).toBeVisible();
  await page.screenshot({ path: shot('7-commit-confirmed') });
});

test('D1-R2：提交期间授权被撤销——pending 不含项目、Inventory 不可读，绝不显示已登记', async ({ page }) => {
  const state = await mock(page, { revokeDuring: true });
  await page.goto('/#/projects');
  const form = await register(page, { province: '330000', prefecture: '330400', county: '330481' });
  await form.getByTestId('registration-submit').click();
  await expect(form.getByTestId('registration-error')).toContainText('结果待核实');
  await expect(page.getByTestId('registration-confirmed')).toHaveCount(0);
  await expect(form).toBeVisible();
  await expect(form.getByLabel('区县')).toHaveValue('330481');
  expect(state.posts).toBe(1);
  await page.screenshot({ path: shot('8-revoked-unverified') });
});

test('D1-R2：已由其他操作登记（409）——显示服务端实际行政区，声明本次表单未应用', async ({ page }) => {
  await mock(page, { registeredElsewhere: 440300 });
  await page.goto('/#/projects');
  const form = await register(page, { province: '330000', prefecture: '330400', county: '330481' });
  await form.getByTestId('registration-submit').click();
  const ok = page.getByTestId('registration-confirmed');
  await expect(ok).toContainText('已由其他操作登记');
  await expect(ok).toContainText('广东省 深圳市');
  await expect(ok).toContainText('本次表单内容未被应用');
  await expect(form).toBeHidden();
});

for (const [name, failWith, extra] of [
  ['通用 503', { status: 503, error: 'database unavailable' }, {}],
  ['403', { status: 403, error: 'Forbidden' }, {}],
  ['409 但 Inventory 读取失败', { status: 409, error: 'project is already registered in Inventory' }, { failProjects: true }],
  ['网络超时', 'timeout', {}],
] as const) {
  test(`D1-R2：${name} 不产生虚假成功提示，保留草稿`, async ({ page }) => {
    const state = await mock(page, { failWith, ...extra });
    await page.goto('/#/projects');
    const form = await register(page, { province: '330000', prefecture: '330400', county: '330481' });
    await form.getByTestId('registration-submit').click();
    await expect(form.getByTestId('registration-error')).toContainText('结果待核实');
    await expect(form.getByTestId('registration-error')).not.toContainText('未做任何修改');
    await expect(page.getByTestId('registration-confirmed')).toHaveCount(0);
    await expect(form.getByLabel('区县')).toHaveValue('330481');
    await expect(form.getByTestId('registration-submit')).toBeEnabled();
    expect(state.posts).toBe(1);
  });
}

test('D1-R2：401 会话失效——回到登录页，没有任何「已登记」提示，也不重发请求', async ({ page }) => {
  const state = await mock(page, { failWith: { status: 401, error: 'session required' } });
  await page.goto('/#/projects');
  const form = await register(page, { province: '330000', prefecture: '330400', county: '330481' });
  await form.getByTestId('registration-submit').click();
  await expect(page.getByRole('link', { name: '账号登录' })).toBeVisible();
  await expect(page.getByText(/已登记（服务端确认）|已由其他操作登记/)).toHaveCount(0);
  expect(state.posts).toBe(1);
});

test('WebGL 不可用时首页回退二维并仍显示已登记项目的行政区统计', async ({ page }) => {
  await page.addInitScript(() => {
    const original = HTMLCanvasElement.prototype.getContext;
    HTMLCanvasElement.prototype.getContext = function (this: HTMLCanvasElement, kind: string, ...args: unknown[]) {
      if (kind.includes('webgl')) return null;
      return original.apply(this, [kind, ...args] as Parameters<typeof original>);
    } as typeof original;
  });
  await mock(page, { registered: true });
  await page.goto('/#/overview');
  await expect(page.getByTestId('webgl-fallback')).toBeVisible();
  await expect(page.getByTestId('spatial-2d')).toBeVisible();
  await expect(page.locator('[data-kpi=registered] .home-kpi-value')).toHaveText('2台');
  await expect(page.locator('[data-kpi=real-connected] .home-kpi-value')).toHaveText('0台');
});

for (const [w, h] of [[1280, 720], [1366, 768], [1920, 1080]] as const) {
  test(`登记表单在 ${w}×${h} 可用`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: h });
    await mock(page);
    await page.goto('/#/projects');
    const form = await register(page, { province: '330000', prefecture: '330400', county: '330481' });
    for (const name of ['registration-submit']) {
      const b = form.getByTestId(name);
      await b.scrollIntoViewIfNeeded();
      await expect(b).toBeInViewport();
      await expect(b).toBeEnabled();
    }
    await expect(form.getByRole('button', { name: '取消' })).toBeInViewport();
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    await page.screenshot({ path: shot(`6-form-${w}x${h}`) });
  });
}
