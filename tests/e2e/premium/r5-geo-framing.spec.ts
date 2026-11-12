// R5-UX-R1 map framing and layer acceptance (real browser, mocked API — no server writes).
// Counter-proof: on the baseline 973e134 these tests fail (country fill 0.62 of the
// canvas width with 188–444 px side margins; the national merged-side-flow wall
// stays visible at province/city/county level). Evidence and per-size numbers are
// in docs/04-前端与可视化/R5-UX-R1-地图构图与镜头优化报告.md.
import { test, expect, type Page } from '@playwright/test';
import { writeFileSync } from 'node:fs';
import { api, captureScenes, framing, located, locatedAssets, main, probe, settled, unlocated, unlocatedAssets } from './r5-geo-helpers';

const SIZES = [
  [1366, 768],
  [1653, 1186],
  [1920, 1080],
  [2560, 1440],
] as const;

/** The framed region fills the safe frame in its binding dimension and stays inside the canvas. */
function expectFilled(f: Awaited<ReturnType<typeof framing>>, label: string) {
  expect(f.fill, `${label} 绑定方向填充率 ≥ 0.6（${f.fillW}/${f.fillH}）`).toBeGreaterThanOrEqual(0.6);
  for (const [k, v] of Object.entries(f.margins)) expect(v, `${label} ${k} 边距 ≥ 0`).toBeGreaterThanOrEqual(-1);
  // Centred horizontally inside the usable (uncovered) canvas.
  expect(Math.abs(f.margins.left - f.margins.right), `${label} 左右居中`).toBeLessThanOrEqual(Math.max(8, f.usable.width * 0.02));
}

async function open(page: Page, region: number, size: readonly [number, number] = [1920, 1080]) {
  await page.setViewportSize({ width: size[0], height: size[1] });
  await page.goto(region === 100000 ? '/#/overview' : `/#/overview?region=${region}`);
  await settled(page, region);
}

test.beforeEach(async ({ page }) => {
  await captureScenes(page);
  await api(page, located, locatedAssets);
});

// 1. National fill ratio and margins at four resolutions (screenshot + numbers attached).
for (const size of SIZES)
  test(`1 全国 ${size[0]}×${size[1]}：投影填充率与边距，全国侧墙可见`, async ({ page }) => {
    await open(page, 100000, size);
    const f = await framing(page);
    expectFilled(f, '全国');
    // Before R5 the country spanned a fixed 62 % of the canvas width at every size.
    expect(f.fillW, '全国宽度填充高于旧的固定 0.62').toBeGreaterThan(0.62);
    const p = await probe(page);
    expect(p.walls).toEqual({ count: 1, effectivelyVisible: 1, inScene: 1 });
    const out = test.info().outputPath(`r5-country-${size[0]}x${size[1]}`);
    writeFileSync(out + '.json', JSON.stringify(f));
    await page.locator('[data-testid=spatial-stage]').screenshot({ path: out + '.png' });
    await test.info().attach(`country-${size[0]}x${size[1]}`, { path: out + '.png', contentType: 'image/png' });
    await test.info().attach(`country-${size[0]}x${size[1]}-metrics`, { body: JSON.stringify(f), contentType: 'application/json' });
  });

// 2 + 6. Beijing drill-down hides the national wall; returning to the country restores it.
test('2/6 北京 110000：全国侧墙不可见；返回全国后恢复且不重复', async ({ page }) => {
  await open(page, 100000);
  await page.getByRole('button', { name: '重置全国视角' }).waitFor();
  await page.goto('/#/overview?region=110000');
  await settled(page, 110000);
  let p = await probe(page);
  expect(p.walls, '省级：全国侧墙存在但不渲染').toEqual({ count: 1, effectivelyVisible: 0, inScene: 1 });
  expectFilled(await framing(page), '北京');
  await page.locator('[data-testid=spatial-stage]').screenshot({ path: test.info().outputPath('r5-110000.png') });
  await page.getByRole('button', { name: '返回全国' }).first().click();
  await settled(page, 100000);
  p = await probe(page);
  expect(p.walls, '返回全国：侧墙恢复，仍只有 1 个').toEqual({ count: 1, effectivelyVisible: 1, inScene: 1 });
});

// 3. Zhejiang → Jiaxing → Haining by clicking the map labels (real drill path).
test('3 浙江 330000 → 嘉兴 330400 → 海宁 330481：逐级下钻，侧墙隐藏，取景充满', async ({ page }) => {
  await open(page, 100000);
  for (const [code, label] of [
    [330000, null],
    [330400, '嘉兴市'],
    [330481, '海宁市'],
  ] as const) {
    if (label === null) await page.goto('/#/overview?region=330000');
    else await page.locator('.map-region', { hasText: label }).first().click();
    await settled(page, code);
    await expect(page).toHaveURL(new RegExp(`region=${code}`));
    const f = await framing(page);
    expectFilled(f, String(code));
    expect(f.preset).toBe(code === 330000 ? 'PROVINCE_CONTEXT' : 'CITY_CONTEXT');
    const p = await probe(page);
    expect(p.walls.effectivelyVisible, `${code} 全国侧墙不可见`).toBe(0);
    await page.locator('[data-testid=spatial-stage]').screenshot({ path: test.info().outputPath(`r5-${code}.png`) });
  }
  // Haining: the located project's markers are inside the frame.
  await expect(page.locator('.map-project', { hasText: 'Project-A' }).first()).toBeVisible();
});

// 4. Different shapes: wide Guangdong, Hainan with the remote 三沙 archipelago.
for (const code of [440000, 460000])
  test(`4 ${code}：不同形状省份按主体陆地取景（离岛不把主体压缩到角落）`, async ({ page }) => {
    await open(page, code);
    const f = await framing(page);
    expectFilled(f, String(code));
    expect((await probe(page)).walls.effectivelyVisible).toBe(0);
    await page.locator('[data-testid=spatial-stage]').screenshot({ path: test.info().outputPath(`r5-${code}.png`) });
  });

// 5. Operations / cinematic switch keeps the framing; operations looks down more steeply.
test('5 运营/立体视角切换：两种视角都充满取景框，运营视角俯角更大', async ({ page }) => {
  await open(page, 330000);
  const ops = await page.evaluate(() => (window as unknown as { __spatialDiagnostics: () => { camera: { elevation: number } } }).__spatialDiagnostics().camera.elevation);
  await page.getByRole('button', { name: '立体视角' }).click();
  await expect.poll(async () => (await framing(page)).preset).toBe('PROVINCE_CINEMATIC');
  await settled(page, 330000);
  expectFilled(await framing(page), '立体');
  const cine = await page.evaluate(() => (window as unknown as { __spatialDiagnostics: () => { camera: { elevation: number } } }).__spatialDiagnostics().camera.elevation);
  expect(ops, '省级运营视角俯角 ≥ 55°').toBeGreaterThanOrEqual((55 * Math.PI) / 180);
  expect(ops).toBeGreaterThan(cine);
  await page.getByRole('button', { name: '运营视角' }).click();
  await expect.poll(async () => (await framing(page)).preset).toBe('PROVINCE_CONTEXT');
  await settled(page, 330000);
  expectFilled(await framing(page), '运营');
  expect((await probe(page)).walls.effectivelyVisible).toBe(0);
});

// 7. Inspector open/close, window resize, and「适配当前区域」.
test('7 Inspector 开闭、窗口 Resize、适配当前区域：重新取景，层级与 URL 不变', async ({ page }) => {
  await open(page, 330000, [1366, 768]);
  // Inspector drawer (narrow layout) covers the right edge → the framed region moves left of it.
  await page.locator('[data-testid=home-inspector] .plan-item[data-project-id=P001]').click();
  await expect(page.getByTestId('project-panel')).toBeVisible();
  await expect(main(page)).toHaveAttribute('data-scene-state', 'PROJECT_FOCUS', { timeout: 20000 });
  await expect.poll(async () => (await framing(page)).inset, '抽屉打开时相机让出抽屉宽度').toBeGreaterThan(0);
  await page.keyboard.press('Escape'); // close detail → back to region framing with drawer state settled
  await settled(page, 330000);
  expectFilled(await framing(page), '关闭详情后');
  // Resize.
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.waitForTimeout(400);
  await settled(page, 330000);
  expectFilled(await framing(page), 'Resize 后');
  // Drag the map away, then fit the current region.
  const url = page.url();
  const box = (await page.locator('.map-canvas canvas').boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down({ button: 'right' });
  await page.mouse.move(box.x + box.width / 2 + 320, box.y + box.height / 2 + 160, { steps: 8 });
  await page.mouse.up({ button: 'right' });
  await page.mouse.wheel(0, 600);
  await page.waitForTimeout(800);
  const moved = await framing(page);
  expect(Math.abs(moved.margins.left - moved.margins.right), '拖动后已偏离').toBeGreaterThan(40);
  await page.getByRole('button', { name: '适配当前区域' }).click();
  await settled(page, 330000);
  const fitted = await framing(page);
  expectFilled(fitted, '适配当前区域');
  expect(page.url(), 'URL 不变').toBe(url);
  await expect(main(page)).toHaveAttribute('data-adcode', '330000');
});

test('7b 窄屏 Inspector 抽屉开→关：抽屉关闭后相机归还整幅画布并重新居中', async ({ page }) => {
  await open(page, 330000, [1366, 768]);
  await page.locator('[data-testid=home-inspector] .plan-item[data-project-id=P001]').click();
  await expect(page.getByTestId('project-panel')).toBeVisible();
  await expect(main(page)).toHaveAttribute('data-scene-state', 'PROJECT_FOCUS', { timeout: 20000 });
  await page.keyboard.press('Escape');
  await settled(page, 330000);
  // Drawer closed again: full canvas is usable, region centred in it.
  const f = await framing(page);
  expect(f.inset).toBe(0);
  expectFilled(f, '抽屉关闭');
});

// 8. Esc, URL history, identity switch, 2D degradation.
test('8 Esc 与 URL 历史：逐级返回，侧墙状态随层级正确', async ({ page }) => {
  await open(page, 330481);
  await page.keyboard.press('Escape');
  await settled(page, 330400);
  await expect(page).toHaveURL(/region=330400/);
  expect((await probe(page)).walls.effectivelyVisible).toBe(0);
  await page.goBack();
  await settled(page, 330481);
  await page.goto('/#/overview');
  await settled(page, 100000);
  expect((await probe(page)).walls.effectivelyVisible).toBe(1);
});

test('8b 2D 降级：?view=2d 不加载三维，地图正常', async ({ page }) => {
  const modules: string[] = [];
  page.on('request', (r) => modules.push(r.url()));
  await page.goto('/#/overview?view=2d&region=330000');
  await expect(page.locator('.stage2d-map path').first()).toBeVisible();
  expect(modules.filter((u) => /MapSceneManager|SpatialStage3D/.test(u))).toEqual([]);
});

// 9. GPU resources and draw calls: drill/return cycles do not leak; no extra draw calls.
test('9 GPU 资源与 Draw Calls：多次下钻/返回无泄漏，侧墙不重复', async ({ page }) => {
  await open(page, 100000);
  await page.waitForTimeout(500);
  const cold = await probe(page);
  const cycle = async () => {
    await page.goto('/#/overview?region=330481');
    await settled(page, 330481);
    await page.getByRole('button', { name: '返回全国' }).first().click();
    await settled(page, 100000);
    await page.waitForTimeout(500);
    return probe(page);
  };
  // The first drill caches shared marker/label geometry once (baseline 973e134: 82 → 85, then flat);
  // a leak shows as growth between later identical cycles.
  const first = await cycle();
  let after = first;
  for (let i = 0; i < 3; i++) after = await cycle();
  expect(after.walls).toEqual({ count: 1, effectivelyVisible: 1, inScene: 1 });
  expect(after.geometries, `geometries cold ${cold.geometries} / 1st ${first.geometries} / 4th ${after.geometries}`).toBe(first.geometries);
  expect(after.textures).toBe(first.textures);
  // Baseline 973e134: country 120 draw calls (SwiftShader, 1920×1080).
  expect(after.drawCalls, '全国 draw calls 不高于基线 120').toBeLessThanOrEqual(120);
  writeFileSync(test.info().outputPath('r5-gpu.json'), JSON.stringify({ cold, first, after }));
});

test('9b 资源加载失败后重试：全国侧墙与省份不重复', async ({ page }) => {
  let fail = 1;
  await page.route('**/geo/china/100000.json', (r) => (fail-- > 0 ? r.fulfill({ status: 500, body: 'x' }) : r.fallback()));
  await page.goto('/#/overview');
  await expect(page.getByTestId('scene-resource-error')).toBeVisible();
  await page.getByTestId('scene-resource-error').getByRole('button', { name: '重试' }).click();
  await settled(page, 100000);
  const p = await probe(page);
  expect(p.walls).toEqual({ count: 1, effectivelyVisible: 1, inScene: 1 });
  expect(p.countryRegionsVisible).toBe(34);
  await expect(page.locator('.province-summary')).toHaveCount(34);
});

// D. Labels: no repeated「· 0台」; hover still shows the real count; data attribute unchanged.
test('D 无数据省份不重复显示「0台」；悬停仍显示实际数量；有数据省份显示数量', async ({ page }) => {
  await open(page, 100000);
  const labels = await page.locator('.province-summary').evaluateAll((es) =>
    es.map((e) => ({ code: (e as HTMLElement).dataset.provinceCode, text: e.textContent, count: Number((e as HTMLElement).dataset.deviceCount), title: (e as HTMLElement).title }))
  );
  expect(labels).toHaveLength(34);
  expect(labels.filter((l) => /0台/.test(l.text ?? '')), '无「· 0台」').toEqual([]);
  const zj = labels.find((l) => l.code === '330000')!;
  expect(zj.count).toBe(2);
  expect(zj.text).toBe('浙江 · 2台');
  const bj = labels.find((l) => l.code === '110000')!;
  expect(bj.text).toBe('北京');
  expect(bj.title).toBe('北京市 · 0台');
  // Hover Xinjiang (large, unambiguous region): its label shows the real count.
  const centers = await page.evaluate(() => (window as unknown as { __spatialDiagnostics: () => { regionCenters: { adcode: number; x: number; y: number }[] } }).__spatialDiagnostics().regionCenters);
  const xj = centers.find((c) => c.adcode === 650000)!;
  await page.mouse.move(xj.x - 30, xj.y + 10);
  await expect(page.locator('.province-summary[data-province-code="650000"]')).toHaveText('新疆 · 0台');
});

// 10. Every asset unlocated: explicit notice, no markers, no fabricated coordinates.
test('10 全部资产无地理归属：明确提示尚未定位，地图不生成任何标记', async ({ page }) => {
  await page.unrouteAll({ behavior: 'ignoreErrors' });
  await api(page, unlocated, unlocatedAssets, 'r5-unlocated');
  await page.goto('/#/overview');
  await settled(page, 100000);
  await expect(page.getByTestId('home-unlocated')).toContainText('尚未定位');
  await expect(page.locator('.map-project, .inventory-project-marker')).toHaveCount(0);
  const counts = await page.locator('.province-summary').evaluateAll((es) => es.map((e) => Number((e as HTMLElement).dataset.deviceCount)));
  expect(counts.every((c) => c === 0)).toBe(true);
  await expect(page.locator('[data-kpi=registered] .home-kpi-value')).toHaveText('3台'); // statistics untouched
  // A located inventory does not show the notice.
  await page.unrouteAll({ behavior: 'ignoreErrors' });
  await api(page, located, locatedAssets);
  await page.reload();
  await settled(page, 100000);
  await expect(page.getByTestId('home-unlocated')).toHaveCount(0);
});
