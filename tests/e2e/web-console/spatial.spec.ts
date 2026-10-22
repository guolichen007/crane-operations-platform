import { test, expect, type Page } from '@playwright/test';
const read = (page: Page) => page.evaluate(() => window.__spatialDiagnostics!());
async function load(page: Page) {
  await page.goto('/');
  await expect(page.locator('.scene-loading')).toBeHidden();
  await page.waitForFunction(() => !window.__spatialDiagnostics!().intro.active);
}
test('默认透视、厚度、性能与统一动效时钟', async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  await load(page);
  const a = await read(page);
  expect(a.camera.fov).toBeGreaterThanOrEqual(34);
  expect(a.projectedRatio).toBeGreaterThanOrEqual(0.4);
  expect(a.sideSample.pixels).toBeGreaterThanOrEqual(6);
  await page.waitForTimeout(1100);
  const b = await read(page);
  expect(b.animation.sideFlowPhase).toBeGreaterThan(a.animation.sideFlowPhase);
  expect(b.animation.spatialBeacons).toHaveLength(0);
  expect(b.picks.every((p) => p.kind === 'cluster')).toBe(true);
  expect(b.animation.network.heads).not.toEqual(a.animation.network.heads);
  await page.keyboard.press('Control+Shift+D');
  await page.waitForTimeout(1100);
  const text = await page.getByTestId('debug-panel').innerText();
  expect(Number(text.match(/Draw calls\n(\d+)/)?.[1])).toBeLessThan(520);
});
test('切换观察角度保留行政路径及项目面板', async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  await load(page);
  await page.locator('.plan-item').first().click();
  await expect(page.locator('main')).toHaveAttribute('data-scene-state', 'PROJECT_FOCUS');
  const panel = page.getByTestId('project-panel');
  await expect(panel).toBeVisible();
  const text = await panel.innerText();
  const before = await read(page);
  await page.getByRole('button', { name: '立体视角', exact: true }).click();
  await expect(page.locator('main')).toHaveAttribute('data-camera-preset', 'PROJECT_CINEMATIC');
  const after = await read(page);
  expect(after.adcode).toBe(before.adcode);
  expect(await panel.innerText()).toBe(text);
  await page.getByRole('button', { name: '查看项目沙盘', exact: true }).click();
  await expect(page.locator('main')).toHaveAttribute('data-camera-preset', 'PROJECT_CINEMATIC');
  await page.getByRole('button', { name: '运营视角', exact: true }).click();
  await expect(page.locator('main')).toHaveAttribute('data-camera-preset', 'PROJECT_FOCUS');
  await expect(panel).toBeVisible();
});
test('Beacon 柱中部打开项目，具体行车顶端打开单机', async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  await load(page);
  await page.locator('[data-region-code="330000"]').click();
  await expect(page.locator('main')).toHaveAttribute('data-scene-state', 'REGION_IDLE');
  const p = (await read(page)).picks.find((p) => p.kind === 'project')!;
  await page.mouse.click(p.x, p.y);
  await expect(page.getByTestId('project-panel')).toBeVisible();
  expect((await read(page)).adcode).toBe(330000);
  await page.getByRole('button', { name: '查看项目沙盘', exact: true }).click();
  await expect(page.locator('main')).toHaveAttribute('data-scene-state', 'PROJECT_FOCUS');
  const d = (await read(page)).picks.find((p) => p.kind === 'device')!;
  const tip = d.span![1]!;
  await page.mouse.click(tip.x, tip.y);
  await expect(page.getByTestId('device-panel')).toHaveAttribute('data-device-id', d.id);
  await expect(page.locator('main')).toHaveAttribute('data-camera-preset', 'DEVICE_FOCUS');
  await expect(page.locator('main')).toHaveAttribute('data-scene-state', 'PROJECT_FOCUS');
  await page.waitForTimeout(800);
  await expect
    .poll(async () => (await read(page)).focusedAssetBounds!.heightRatio)
    .toBeGreaterThanOrEqual(0.18);
  expect((await read(page)).focusedAssetBounds!.heightRatio).toBeLessThanOrEqual(0.28);
});

test('逐级进入云南红河个旧后，C 机不被工具按钮遮挡', async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  await load(page);
  for (const code of [530000, 532500, 532501]) {
    await page.locator(`[data-region-code="${code}"]`).click();
    await expect(page.locator('main')).toHaveAttribute('data-scene-state', 'REGION_IDLE');
  }
  await page.setViewportSize({ width: 1366, height: 768 });
  await page.waitForTimeout(1100);
  const c = (await read(page)).picks.find((p) => p.id === 'SAMPLE-006-C')!;
  const blocking = await page.evaluate(
    ({ x, y }) =>
      document.elementFromPoint(x, y)?.closest('button,.left-context,.right-context')?.className ??
      null,
    c
  );
  expect(blocking).toBeNull();
  await page.mouse.click(c.x, c.y);
  await expect(page.getByTestId('device-panel')).toHaveAttribute('data-device-id', c.id);
  await expect(page.locator('main')).toHaveAttribute('data-camera-preset', 'DEVICE_FOCUS');
  await expect(page.locator('main')).toHaveAttribute('data-scene-state', 'PROJECT_FOCUS');
  await page.waitForTimeout(800);
});

test('海宁逐级点击时按最终动画尺寸取景', async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  await load(page);
  for (const code of [330000, 330400, 330481]) {
    await page.locator(`[data-region-code="${code}"]`).click();
    await expect(page.locator('main')).toHaveAttribute('data-scene-state', 'REGION_IDLE');
  }
  const d = (await read(page)).picks.find((p) => p.kind === 'device')!;
  await page.mouse.click(d.x, d.y);
  await expect(page.locator('main')).toHaveAttribute('data-camera-preset', 'DEVICE_FOCUS');
  await expect(page.locator('main')).toHaveAttribute('data-scene-state', 'PROJECT_FOCUS');
  await page.waitForTimeout(900);
  const h = (await read(page)).focusedAssetBounds!.heightRatio;
  expect(h).toBeGreaterThanOrEqual(0.18);
  expect(h).toBeLessThanOrEqual(0.28);
});
