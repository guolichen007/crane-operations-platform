// H1 homepage acceptance (real browser, mocked API — no server writes).
// Covers: five-resolution layout measurement with the unified net-map-area metric
// (H1-0 design §2), 2D / 3D / WebGL-failure fallback, KPI actions bound to their
// API data sources, project/asset switching through the URL, API failure with
// retry, and slow network. Screenshots and metrics are attached as evidence.
import { test, expect, type Page } from '@playwright/test';
import { readFileSync, writeFileSync } from 'node:fs';
import { cloudDevice, cloudAlarm } from '../../../apps/web-console/tests/cloud-fixtures';

const example = JSON.parse(readFileSync('deploy/inventory/inventory.example.yaml', 'utf8'));
const session = {
  userId: 'acc',
  displayName: '验收员',
  projects: example.projects.map((p: { id: string; name: string }) => ({
    projectId: p.id,
    projectName: p.name,
    role: 'viewer',
  })),
  csrfToken: 'synthetic-csrf-test-only',
  expiresAt: '2099-01-01T00:00:00Z',
  idleExpiresAt: '2099-01-01T00:00:00Z',
};

type Opts = { failProjects?: number; delayMs?: number; alarm?: boolean };
/** Inventory fixture: P001 located in Haining with one ALARM device bound to SIM-P001-01. */
async function fixture(page: Page, opts: Opts = {}) {
  const calls: string[] = [];
  let projectFailures = opts.failProjects ?? 0;
  await page.route('**/auth/session', (r) => r.fulfill({ json: session }));
  const projects = example.projects.map((p: { id: string }, i: number) => ({
    ...p,
    adminCode: i === 0 ? 330481 : null,
    simulatedCount: 0,
    runtimeDeviceCount: 0,
    realConnectedCount: 0,
    assetDeviceCount: example.assetDevices.filter(
      (a: { projectId: string }) => a.projectId === p.id
    ).length,
  }));
  const assets = example.assetDevices.map((a: { projectId: string }, i: number) =>
    i === 0
      ? {
          ...a,
          runtimeSource: 'SIMULATION',
          runtimeDeviceId: 'SIM-P001-01',
          connectivity: 'MQTT_CONNECTED',
        }
      : { ...a, runtimeSource: null, runtimeDeviceId: null, connectivity: null }
  );
  const shadow = cloudDevice('SIM-P001-01');
  if (opts.alarm !== false) shadow.reported.alarmState = 'ACTIVE';
  await page.route('**/api/v1/**', async (r) => {
    const path = new URL(r.request().url()).pathname;
    calls.push(path);
    if (opts.delayMs) await new Promise((res) => setTimeout(res, opts.delayMs));
    if (path === '/api/v1/projects' && projectFailures > 0) {
      projectFailures--;
      return r.fulfill({ status: 503, json: { error: 'inventory unavailable' } });
    }
    return r.fulfill({
      json:
        path === '/api/v1/projects'
          ? projects
          : path === '/api/v1/assets/devices'
            ? assets
            : path === '/api/v1/devices'
              ? [shadow]
              : path === '/api/v1/alarms'
                ? [cloudAlarm()]
                : {},
    });
  });
  return calls;
}
const main = (page: Page) => page.locator('main.overview');

async function measure(page: Page) {
  return page.evaluate(() => {
    const stage = document.querySelector('[data-testid=spatial-stage]')!.getBoundingClientRect();
    const vw = innerWidth,
      vh = innerHeight;
    const x0 = Math.max(0, stage.left),
      x1 = Math.min(vw, stage.right),
      y0 = Math.max(0, stage.top),
      y1 = Math.min(vh, stage.bottom);
    let area = Math.max(0, x1 - x0) * Math.max(0, y1 - y0);
    for (const el of document.querySelectorAll(
      '[data-testid=spatial-stage] .map-topline > *, [data-testid=spatial-stage] .map-view-switch, [data-testid=spatial-stage] .observation-switch, [data-testid=spatial-stage] .scene-mode-dock, [data-testid=spatial-stage] .map-tools, [data-testid=spatial-stage] .map-attribution, [data-testid=spatial-stage] .south-sea-inset-note, [data-testid=spatial-stage] .stage2d-legend'
    )) {
      const r = el.getBoundingClientRect();
      area -=
        Math.max(0, Math.min(x1, r.right) - Math.max(x0, r.left)) *
        Math.max(0, Math.min(y1, r.bottom) - Math.max(y0, r.top));
    }
    let minFont = 99;
    for (const e of document.querySelectorAll('main.home-oc *')) {
      if (e.closest('.map-canvas, .stage2d-labels')) continue; // map labels are map content
      if (![...e.childNodes].some((n) => n.nodeType === 3 && n.textContent!.trim())) continue;
      const r = e.getBoundingClientRect();
      if (!r.width || r.top > vh) continue;
      minFont = Math.min(minFont, parseFloat(getComputedStyle(e).fontSize));
    }
    return {
      netMapPct: +((area / (vw * vh)) * 100).toFixed(1),
      overflowX: document.documentElement.scrollWidth - vw,
      minFont,
      kpiCards: document.querySelectorAll('[data-testid=home-kpis] .home-kpi').length,
    };
  });
}

const thresholds: Record<string, number> = {
  '1280x720': 40,
  '1366x768': 40,
  '1440x900': 35,
  '1920x1080': 35,
  '2560x1440': 35,
};
for (const [w, h] of [
  [1280, 720],
  [1366, 768],
  [1440, 900],
  [1920, 1080],
  [2560, 1440],
] as const)
  test(`H1 布局 ${w}×${h}：地图净可见面积达标、无横向溢出、字号 ≥10、KPI ≤5`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: h });
    await fixture(page);
    await page.goto('/#/overview');
    await expect(page.locator('.province-summary')).toHaveCount(34);
    await expect(page.locator('.scene-loading')).toBeHidden();
    await page.waitForTimeout(1500);
    const m = await measure(page);
    writeFileSync(
      test.info().outputPath(`metrics-${w}x${h}.json`),
      JSON.stringify({ viewport: `${w}x${h}`, view: '3d', ...m })
    );
    await test
      .info()
      .attach(`metrics-${w}x${h}`, { body: JSON.stringify(m), contentType: 'application/json' });
    await page.screenshot({ path: test.info().outputPath(`home-h1-${w}x${h}-3d.png`) });
    await test.info().attach(`home-h1-${w}x${h}-3d`, {
      path: test.info().outputPath(`home-h1-${w}x${h}-3d.png`),
      contentType: 'image/png',
    });
    expect(m.netMapPct).toBeGreaterThanOrEqual(thresholds[`${w}x${h}`]!);
    expect(m.overflowX).toBeLessThanOrEqual(0);
    expect(m.minFont).toBeGreaterThanOrEqual(10);
    expect(m.kpiCards).toBeLessThanOrEqual(5);
    // 2D view at the same size.
    await page.getByRole('button', { name: '轻量视图' }).click();
    await expect(page.getByTestId('spatial-2d')).toBeVisible();
    await expect(page.locator('.stage2d-map path').first()).toBeVisible();
    writeFileSync(
      test.info().outputPath(`metrics-${w}x${h}-2d.json`),
      JSON.stringify({ viewport: `${w}x${h}`, view: '2d', ...(await measure(page)) })
    );
    await page.screenshot({ path: test.info().outputPath(`home-h1-${w}x${h}-2d.png`) });
    await test.info().attach(`home-h1-${w}x${h}-2d`, {
      path: test.info().outputPath(`home-h1-${w}x${h}-2d.png`),
      contentType: 'image/png',
    });
  });

test('二维视图不加载 Three：?view=2d 不请求 three / MapSceneManager / 三维舞台', async ({
  page,
  browser,
}) => {
  await fixture(page);
  const modules: string[] = [];
  page.on('request', (r) => modules.push(r.url()));
  await page.goto('/#/overview?view=2d');
  await expect(page.getByTestId('spatial-2d')).toBeVisible();
  await expect(page.locator('.stage2d-map path').first()).toBeVisible();
  await page.waitForTimeout(800);
  // OIDC mode: the homepage's own 3D modules are never requested. (The three
  // library itself is loaded here by the pre-existing login cover animation while
  // the session is being checked — not by the homepage — so it is asserted in the
  // no-auth context below.)
  expect(modules.filter((u) => /MapSceneManager|SpatialStage3D/.test(u))).toEqual([]);
  // Real-mouse drill in 2D writes the same ?region= as 3D.
  await page.locator('.stage2d-map path[data-region-code="330000"]').click();
  await expect(page.getByTestId('spatial-2d')).toHaveAttribute('data-region', '330000');
  await expect(page).toHaveURL(/region=330000/);

  // No-auth (demo) server: nothing loads three.js before the user asks for 3D.
  const ctx = await browser.newContext();
  const demo = await ctx.newPage();
  const demoModules: string[] = [];
  demo.on('request', (r) => demoModules.push(r.url()));
  await demo.goto('http://127.0.0.1:5173/#/overview?view=2d');
  await expect(demo.getByTestId('spatial-2d')).toBeVisible();
  await expect(demo.locator('.stage2d-map path').first()).toBeVisible();
  await demo.waitForTimeout(800);
  expect(demoModules.filter((u) => /MapSceneManager|SpatialStage3D|\/deps\/three/.test(u))).toEqual(
    []
  );
  // Switching to 3D then loads them on demand.
  await demo.getByRole('button', { name: '三维沙盘' }).click();
  await expect(demo.locator('.province-summary')).toHaveCount(34, { timeout: 60000 });
  expect(demoModules.some((u) => /\/deps\/three/.test(u))).toBe(true);
  await ctx.close();
});

test('WebGL 不可用：自动切换二维视图并提示，三维按钮禁用，业务仍可操作', async ({ page }) => {
  await page.addInitScript(() => {
    const original = HTMLCanvasElement.prototype.getContext;
    HTMLCanvasElement.prototype.getContext = function (
      this: HTMLCanvasElement,
      kind: string,
      ...args: unknown[]
    ) {
      if (kind.includes('webgl')) return null;
      return original.apply(this, [kind, ...args] as Parameters<typeof original>);
    } as typeof original;
  });
  await fixture(page);
  await page.goto('/#/overview');
  await expect(page.getByTestId('webgl-fallback')).toBeVisible();
  await expect(page.getByTestId('spatial-2d')).toBeVisible();
  await expect(page.getByRole('button', { name: '三维沙盘' })).toBeDisabled();
  await page.locator('[data-kpi=alarm]').click();
  await expect(main(page)).toHaveAttribute('data-status-filter', 'ALARM');
  await page.screenshot({ path: test.info().outputPath('home-h1-webgl-fallback.png') });
  await test.info().attach('home-h1-webgl-fallback', {
    path: test.info().outputPath('home-h1-webgl-fallback.png'),
    contentType: 'image/png',
  });
});

test('KPI 动作与数据来源：数值来自 API，点击告警 KPI 过滤并写 URL，Inspector 列出该设备', async ({
  page,
}) => {
  const calls = await fixture(page);
  await page.goto('/#/overview');
  for (const p of [
    '/api/v1/projects',
    '/api/v1/assets/devices',
    '/api/v1/devices',
    '/api/v1/alarms',
  ])
    expect.poll(() => calls.includes(p)).toBe(true);
  // 10 inventory devices; one bound SIMULATION device with an ACTIVE alarm.
  await expect(page.locator('[data-kpi=devices] .home-kpi-value')).toContainText('10');
  await expect(page.locator('[data-kpi=alarm] .home-kpi-value')).toContainText('1');
  await expect(page.locator('[data-kpi=alarm]')).toContainText('最近告警记录 1 条（非未处理数）');
  await expect(page.locator('[data-kpi=real-running] .home-kpi-value')).toContainText('0'); // SIM is never counted as REAL
  await page.locator('[data-kpi=alarm]').click();
  await expect(page.locator('[data-kpi=alarm]')).toHaveAttribute('aria-pressed', 'true');
  await expect(page).toHaveURL(/status=ALARM/);
  await expect(page.locator('[data-testid=home-inspector] .inspector-device')).toHaveCount(1);
  // Attention uses the same filter.
  await expect(page.locator('[data-attention=alarm]')).toHaveAttribute('aria-pressed', 'true');
  await page.locator('[data-attention=alarm]').click();
  await expect(main(page)).toHaveAttribute('data-status-filter', 'all');
  await expect(page).not.toHaveURL(/status=/);
  // Unavailable data is not a number and not clickable.
  await expect(page.locator('[data-attention=work-orders]')).toContainText('未接入');
  await expect(page.locator('[data-attention=inspection]')).toContainText('未接入');
});

test('项目/资产切换：Inspector 选择写 ?project= / ?asset=，后退恢复上一个选择，未知资产提示并清理', async ({
  page,
}) => {
  await fixture(page);
  await page.goto('/#/overview');
  await expect(page.locator('.scene-loading')).toBeHidden();
  await page.locator('[data-testid=home-inspector] .plan-item').first().click();
  await expect(page).toHaveURL(/project=P001/);
  await expect(page.getByTestId('project-panel')).toHaveAttribute('data-project-id', 'P001');
  await page.getByTestId('project-panel').locator('[data-device-id]').first().click();
  await expect(page).toHaveURL(/asset=/);
  await expect(page.getByTestId('device-panel')).toBeVisible();
  await page.goBack();
  await expect(page).toHaveURL(/project=P001/);
  await expect(page.getByTestId('project-panel')).toHaveAttribute('data-project-id', 'P001');
  await page.goto('/#/overview?asset=NOPE-404');
  await expect(page.getByTestId('home-notice')).toContainText('NOPE-404');
  await expect.poll(() => page.url()).not.toContain('NOPE-404');
});

for (const [w, h] of [
  [1280, 720],
  [1440, 900],
] as const)
  test(`${w}×${h} 详情抽屉打开时，地图右侧工具（重置/定位/观察模式）不被抽屉遮挡`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: w, height: h });
    await fixture(page);
    await page.goto('/#/overview?project=P001');
    await expect(page.getByTestId('project-panel')).toHaveAttribute('data-project-id', 'P001');
    await expect(page.getByTestId('home-inspector')).toHaveAttribute('role', 'dialog');
    for (const name of ['重置全国视角', '聚焦首个登记项目']) {
      const box = (await page.getByRole('button', { name }).boundingBox())!;
      const hit = await page.evaluate(
        ({ x, y, name }) =>
          document.elementFromPoint(x, y)?.closest('button')?.getAttribute('aria-label') === name,
        { x: box.x + box.width / 2, y: box.y + box.height / 2, name }
      );
      expect(hit, `${name} 未被抽屉遮挡`).toBe(true);
    }
    await page.getByRole('button', { name: '重置全国视角' }).click();
    await expect(main(page)).toHaveAttribute('data-adcode', '100000');
    // Esc closes the drawer (design §3), in 3D and in 2D.
    for (const view of ['3d', '2d']) {
      await page.goto(`/#/overview?project=P001${view === '2d' ? '&view=2d' : ''}`);
      await expect(page.getByTestId('home-inspector')).toHaveAttribute('role', 'dialog');
      if (view === '3d')
        await expect(main(page)).toHaveAttribute('data-scene-state', 'PROJECT_FOCUS');
      else await expect(page.getByTestId('spatial-2d')).toBeVisible();
      await page.keyboard.press('Escape');
      await expect(
        page.locator('[data-testid=home-inspector][role=dialog]'),
        `${view} Esc 关闭抽屉`
      ).toHaveCount(0);
      await expect.poll(() => page.url()).not.toContain('project=');
    }
  });

test('二维视图的项目/资产选择：?project= 深链与 Inspector 点击均打开详情，与三维共用同一选择', async ({
  page,
}) => {
  await fixture(page);
  await page.goto('/#/overview?view=2d&project=P001');
  await expect(page.getByTestId('project-panel')).toHaveAttribute('data-project-id', 'P001');
  await page.getByTestId('project-panel').locator('[data-device-id]').first().click();
  await expect(page).toHaveURL(/asset=/);
  await expect(page.getByTestId('device-panel')).toBeVisible();
  await page.goBack();
  await expect(page.getByTestId('project-panel')).toHaveAttribute('data-project-id', 'P001');
  // Switch to 3D keeps the selection (one store).
  await page.getByRole('button', { name: '三维沙盘' }).click();
  await expect(page.getByTestId('project-panel')).toHaveAttribute('data-project-id', 'P001');
  await expect(page).toHaveURL(/project=P001/);
});

test('API 失败：显示可恢复错误，重试后加载成功', async ({ page }) => {
  await fixture(page, { failProjects: 1 });
  await page.goto('/#/overview');
  await expect(page.getByTestId('home-load-error')).toBeVisible();
  await expect(page.getByTestId('home-kpis')).not.toContainText('NaN');
  await page.getByTestId('home-load-error').getByRole('button', { name: '重试' }).click();
  await expect(page.getByTestId('home-load-error')).toHaveCount(0);
  await expect(page.locator('[data-kpi=devices] .home-kpi-value')).toContainText('10');
});

test('弱网（每个 API 请求延迟 2.5 s）：加载中显示占位而非 0，数据到达后正常', async ({ page }) => {
  await fixture(page, { delayMs: 2500 });
  await page.goto('/#/overview');
  await expect(page.locator('[data-kpi=devices] .home-kpi-value')).toHaveText(/—/);
  await expect(page.getByTestId('home-asof')).toContainText('正在读取');
  await expect(page.locator('[data-kpi=devices] .home-kpi-value')).toContainText('10', {
    timeout: 20000,
  });
  await expect(page.getByTestId('home-asof')).toContainText('数据截至');
});
