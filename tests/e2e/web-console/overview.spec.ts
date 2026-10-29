import { test, expect, type Page } from '@playwright/test';
const read = (page: Page) => page.evaluate(() => window.__spatialDiagnostics!());
async function load(page: Page) {
  await page.goto('/');
  await expect(page.locator('.province-summary')).toHaveCount(34);
  await expect(page.locator('.scene-loading')).toBeHidden();
  await expect(page.locator('.scene-error')).toHaveCount(0);
  await page.waitForFunction(() => !window.__spatialDiagnostics!().intro.active);
}
async function settled(page: Page, state = 'REGION_IDLE') {
  await expect(page.locator('main')).toHaveAttribute('data-scene-state', state);
}
async function reset(page: Page) {
  await page.getByRole('button', { name: '重置全国视角' }).click();
  await settled(page, 'COUNTRY_IDLE');
}
for (const viewport of [
  { width: 1366, height: 768 },
  { width: 1440, height: 900 },
  { width: 1920, height: 1080 },
])
  test(`视觉证据 ${viewport.width}x${viewport.height}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await load(page);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(viewport.width);
    const normal = await read(page);
    await expect(page.locator('.map-project.real,.map-hover-card')).toHaveCount(0);
    await expect(page.locator('.province-summary')).toHaveCount(34);

    expect(normal.country.minX).toBeGreaterThanOrEqual(0);
    expect(normal.country.maxX).toBeLessThanOrEqual(normal.stage.width);
    expect(normal.country.minY).toBeGreaterThanOrEqual(0);
    expect(normal.country.maxY).toBeLessThanOrEqual(normal.stage.height);
    if (viewport.width === 1920) {
      expect(normal.country.pageWidthRatio).toBeGreaterThanOrEqual(0.58);
      expect(normal.country.pageWidthRatio).toBeLessThanOrEqual(0.68);
    }
    await page.screenshot({ path: `test-results-v033/regression/country-${viewport.width}.png` });
    await page.getByRole('button', { name: '设备聚焦', exact: true }).click();
    await page.waitForTimeout(1300);
    const focused = await read(page);
    const zoom = focused.country.width / normal.country.width;
    expect(zoom).toBeGreaterThanOrEqual(1.1);
    expect(zoom).toBeLessThanOrEqual(1.16);
    expect(focused.adcode).toBe(100000);
    expect(focused.country.minX).toBeGreaterThanOrEqual(0);
    expect(focused.country.maxX).toBeLessThanOrEqual(focused.stage.width);
    expect(focused.country.minY).toBeGreaterThanOrEqual(0);
    expect(focused.country.maxY).toBeLessThanOrEqual(focused.stage.height);
    expect(focused.cameraTarget[0]!).toBeGreaterThan(normal.cameraTarget[0]!);
    await page.screenshot({ path: `test-results-v033/regression/assets-${viewport.width}.png` });
    console.log(
      'VISUAL',
      JSON.stringify({ viewport, normal: normal.country, focused: focused.country, zoom })
    );
    await page.keyboard.press('Control+Shift+D');
    await expect(page.getByTestId('debug-panel')).toBeVisible();
    await page.waitForTimeout(1100);
    console.log('PERF', viewport.width, await page.getByTestId('debug-panel').innerText());
  });
test('窗口变化后重新取景保持地图红线', async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 768 });
  await load(page);
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.waitForTimeout(1500);
  expect((await read(page)).country.pageWidthRatio).toBeGreaterThanOrEqual(0.55);
});
test('全国省级汇总点击进入省，首页不再命中实际项目', async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  await load(page);
  const targets = (await read(page)).picks;
  expect(targets.every((p) => p.kind === 'cluster')).toBe(true);
  const target = targets.find((p) => p.id === '330000')!;
  await page.mouse.click(target.x, target.y);
  await settled(page);
  await expect(page.locator('main')).toHaveAttribute('data-adcode', '330000');
});
test('抬升克制及浙江广东省级镜头', async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  await load(page);
  const west = (await read(page)).regionCenters.find((r) => r.adcode === 650000)!;
  await page.mouse.move(west.x - 35, west.y);
  await page.waitForTimeout(350);
  expect((await read(page)).hoverLift).toBeGreaterThanOrEqual(0.02);
  expect((await read(page)).hoverLift).toBeLessThanOrEqual(0.06);
  await page.mouse.move(700, 100);
  for (const code of [330000, 440000]) {
    await page.locator(`[data-region-code="${code}"]`).click();
    await settled(page);
    const d = await read(page);
    expect(d.preset).toBe('PROVINCE_CONTEXT');
    expect(d.region.widthRatio).toBeGreaterThanOrEqual(0.45);
    expect(d.region.widthRatio).toBeLessThanOrEqual(0.58);
    expect(d.selectedLiftMax).toBeGreaterThanOrEqual(0.3);
    expect(d.selectedLiftMax).toBeLessThanOrEqual(0.7);
    await page.screenshot({ path: `test-results-v033/regression/province-${code}.png` });
    await reset(page);
  }
});
for (const fixture of [
  { path: [330000, 330400, 330481], name: 'Project-A' },
  { path: [510000, 513200, 513201], name: 'Project-C' },
  { path: [460000, 469023], name: 'Project-D' },
  { path: [530000, 532500, 532501], name: 'Project-F' },
])
  test(`保留行政区路径 ${fixture.name}`, async ({ page }) => {
    await load(page);
    for (const code of fixture.path) {
      await page.locator(`[data-region-code="${code}"]`).click();
      await settled(page);
      await expect(page.locator('main')).toHaveAttribute('data-adcode', String(code));
    }
    const target = (await read(page)).picks.find((p) => p.kind === 'device')!;
    await page.mouse.click(target.x, target.y);
    await expect(page.getByTestId('device-panel')).toContainText(fixture.name);
    await expect(page.getByTestId('device-panel')).toContainText('DEMO TELEMETRY');
    await settled(page, 'PROJECT_FOCUS');
    await reset(page);
  });
test('全部十台登记行车的项目到单机模拟', async ({ page }) => {
  test.setTimeout(150000);
  await page.setViewportSize({ width: 1920, height: 1080 });
  await load(page);
  let count = 0;
  for (const name of [
    'Project-A',
    'Project-B',
    'Project-C',
    'Project-D',
    'Project-E',
    'Project-F',
  ]) {
    await page.locator('.plan-item').filter({ hasText: name }).click();
    const project = page.getByTestId('project-panel');
    await expect(project).toContainText('REAL PROJECT');
    await expect(project).toContainText('DEMO TELEMETRY');
    await settled(page, 'PROJECT_FOCUS');
    const ids = await project
      .locator('[data-device-id]')
      .evaluateAll((items) => items.map((item) => item.getAttribute('data-device-id')!));
    if (name === 'Project-F') {
      await expect(project.locator('[data-device-id$="-A"]')).toContainText('运行中');
      await expect(project.locator('[data-device-id$="-B"]')).toContainText('维护中');
      await expect(project.locator('[data-device-id$="-C"]')).toContainText('运行中');
      await page.screenshot({ path: 'test-results-v033/regression/yunnan-project.png' });
    }
    for (let i = 0; i < ids.length; i++) {
      if (i > 0) {
        await page.getByRole('button', { name: '返回项目总览', exact: true }).click();
        await expect(project).toBeVisible();
        await settled(page, 'PROJECT_FOCUS');
      }
      await project.locator(`[data-device-id="${ids[i]}"]`).click();
      await settled(page, 'PROJECT_FOCUS');
      const panel = page.getByTestId('device-panel');
      await expect(panel).toHaveAttribute('data-device-id', ids[i]!);
      await expect(panel).toContainText('REAL ASSET · DEMO TELEMETRY');
      for (const text of [
        '负载率',
        '大车 X',
        '小车 Y',
        '起升 Z',
        '抓斗',
        '控制模式',
        '今日运行',
        '最后心跳',
        '尚未投运',
      ])
        await expect(panel).toContainText(text);
      expect((await read(page)).preset).toBe('DEVICE_FOCUS');
      count++;
      if (name === 'Project-F' && i === 2) {
        await page.screenshot({ path: 'test-results-v033/regression/yunnan-C.png' });
        await panel.locator('.archive-plan').scrollIntoViewIfNeeded();
        await expect(panel).toContainText('2026-12');
        await expect(panel).toContainText('2027-02');
        await page.screenshot({ path: 'test-results-v033/regression/yunnan-C-plan.png' });
      }
    }
    await page.getByRole('button', { name: '关闭详情' }).click();
    await reset(page);
  }
  expect(count).toBe(10);
});
test('具体 C 机在 35px 邻域优先于项目与地域', async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  await load(page);
  await page.locator('.plan-item').filter({ hasText: 'Project-F' }).click();
  await settled(page, 'PROJECT_FOCUS');
  const c = (await read(page)).picks.find((p) => p.id === 'SAMPLE-006-C')!;
  await page.mouse.click(c.x, c.y - 35);
  await expect(page.getByTestId('device-panel')).toHaveAttribute('data-device-id', 'SAMPLE-006-C');
  await settled(page, 'PROJECT_FOCUS');
  expect((await read(page)).lastPick?.kind).toBe('device');
  expect((await read(page)).adcode).toBe(100000);
});
test('实际动效截图及真实计划线路隔离', async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  await load(page);
  const a = await read(page);
  await page.screenshot({ path: 'test-results-v033/regression/motion-t0.png' });
  await page.waitForTimeout(1000);
  const b = await read(page);
  await page.screenshot({ path: 'test-results-v033/regression/motion-t1.png' });
  expect(b.animation.network.heads).not.toEqual(a.animation.network.heads);
  expect(b.animation.spatialBeacons).toHaveLength(0);
  expect(b.animation.network.arcCount).toBeLessThanOrEqual(10);
  const routes = b.animation.network.routeIds;
  await page.waitForFunction(
    (previousBatch) => window.__spatialDiagnostics!().animation.network.batch > previousBatch,
    b.animation.network.batch
  );
  expect((await read(page)).animation.network.routeIds).not.toEqual(routes);
  await page.getByRole('button', { name: '匿名登记项目', exact: true }).click();
  await page.waitForTimeout(300);
  const real = await read(page);
  expect(real.animation.network.plannedCount).toBe(6);
  expect(real.animation.network.dynamicCount).toBe(0);
  await page.getByRole('button', { name: '演示设备', exact: true }).click();
  await expect(page.locator('main')).toHaveAttribute('data-source-filter', 'demo');
  expect((await read(page)).picks.some((p) => p.kind === 'project')).toBe(false);
});
test('业务页、真实资产模拟监控与安全的本地远控', async ({ page }) => {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  for (const path of [
    'projects',
    'devices',
    'monitor',
    'alarms',
    'analytics',
    'maintenance',
    'remote',
  ]) {
    await page.goto('/#/' + path);
    await expect(page.locator('.page-heading h1')).toBeVisible();
  }
  await page.goto('/#/monitor?device=SAMPLE-006-C');
  await expect(page.locator('.monitor-layout')).toBeVisible();
  await expect(page.locator('.real-archive-notice')).toContainText('REAL ASSET · DEMO TELEMETRY');
  await page.goto('/#/devices');
  await page.getByRole('textbox', { name: '搜索项目设备' }).fill('SAMPLE-004');
  await expect(page.locator('tbody tr')).toHaveCount(2);
  await page.goto('/#/alarms');
  await page.getByRole('button', { name: '确认告警' }).first().click();
  await expect(page.getByRole('button', { name: '已确认' })).toHaveCount(1);
  await page.goto('/#/remote');
  const external: string[] = [];
  page.on('request', (r) => {
    if (!r.url().startsWith('http://127.0.0.1:5173')) external.push(r.url());
  });
  await page.getByRole('button', { name: '模拟急停', exact: true }).click();
  await expect(page.getByRole('dialog')).toContainText('模拟操作确认');
  await page.getByRole('button', { name: '确认模拟', exact: true }).click();
  await expect(page.locator('.local-log')).toContainText('模拟急停');
  expect(external).toEqual([]);
  expect(errors).toEqual([]);
});
test('完整巡演循环、暂停恢复及手动中止', async ({ page }) => {
  await load(page);
  await page.getByRole('button', { name: '自动巡演', exact: true }).click();
  await expect(page.locator('main')).toHaveAttribute('data-tour', 'playing');
  await page.getByRole('button', { name: '暂停巡演' }).click();
  await expect(page.locator('main')).toHaveAttribute('data-tour', 'paused');
  await page.getByRole('button', { name: '继续', exact: true }).click();
  await expect(page.locator('main')).toHaveAttribute('data-adcode', '330481', { timeout: 23000 });
  await expect(page.getByTestId('project-panel')).toContainText('Project-A', { timeout: 12000 });
  await expect(page.getByTestId('project-panel')).toContainText('Project-F', { timeout: 20000 });
  await expect(page.locator('.tour-controls')).toContainText('全国设备网络 · 持续连接', {
    timeout: 14000,
  });
  await expect(page.locator('.tour-controls')).toContainText('全国设备网络 · 资产全景', {
    timeout: 12000,
  });
  await page.locator('.overview-intro h1').click();
  await expect(page.locator('main')).toHaveAttribute('data-tour', 'stopped');
});

test('单机视图窗口变化保留 DEVICE_FOCUS，ESC 返回全国', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await load(page);
  await page.locator('.plan-item').filter({ hasText: 'Project-F' }).click();
  await settled(page, 'PROJECT_FOCUS');
  await page.getByTestId('project-panel').locator('[data-device-id$="-C"]').click();
  await settled(page, 'PROJECT_FOCUS');
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.waitForTimeout(1200);
  expect((await read(page)).preset).toBe('DEVICE_FOCUS');
  await expect(page.getByTestId('device-panel')).toHaveAttribute('data-device-id', 'SAMPLE-006-C');
  await page.keyboard.press('Escape');
  await settled(page, 'COUNTRY_IDLE');
  expect((await read(page)).preset).toBe('COUNTRY_OVERVIEW');
  await expect(page.getByTestId('device-panel')).toHaveCount(0);
});
test('巡演中途点击立即退出且不再推进项目步骤', async ({ page }) => {
  await load(page);
  await page.getByRole('button', { name: '自动巡演', exact: true }).click();
  await expect(page.locator('main')).toHaveAttribute('data-adcode', '330000', { timeout: 15000 });
  await page.locator('.overview-intro h1').click();
  await expect(page.locator('main')).toHaveAttribute('data-tour', 'stopped');
  await page.waitForTimeout(13000);
  await expect(page.getByTestId('project-panel')).toHaveCount(0);
  await expect(page.locator('main')).toHaveAttribute('data-adcode', '330000');
});
