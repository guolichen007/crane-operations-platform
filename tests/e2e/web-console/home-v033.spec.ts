import { test, expect, type Page } from '@playwright/test';
async function load(page: Page) {
  await page.goto('/');
  await expect(page.locator('.scene-loading')).toBeHidden();
  await page.waitForFunction(() => !window.__spatialDiagnostics!().intro.active);
}
for (const viewport of [
  { width: 1920, height: 1080 },
  { width: 1440, height: 900 },
  { width: 1366, height: 768 },
])
  test(`首页省级汇总、数量和稳定标签 ${viewport.width}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await load(page);
    await expect(page.locator('.map-project.real,.map-hover-card')).toHaveCount(0);
    const labels = await page
      .locator('.province-summary')
      .evaluateAll((es) =>
        es.map((e) => ({
          text: e.textContent,
          visible: getComputedStyle(e).visibility === 'visible',
          count: Number((e as HTMLElement).dataset.deviceCount),
        }))
      );
    expect(labels.filter((l) => l.visible).length).toBeGreaterThanOrEqual(24);
    expect(labels.reduce((s, l) => s + l.count, 0)).toBe(54);
    for (const name of ['浙江', '江苏', '广东', '山东', '四川', '云南'])
      expect(labels.some((l) => l.visible && l.text?.startsWith(name + ' · '))).toBe(true);
    const box = (await page.locator('[data-province-code="330000"]').boundingBox())!;
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.waitForTimeout(400);
    const stable = await page.evaluate(async () => {
      const samples: string[] = [];
      for (let i = 0; i < 90; i++) {
        await new Promise(requestAnimationFrame);
        samples.push(
          JSON.stringify(
            [...document.querySelectorAll<HTMLElement>('.province-summary')].map((e) => [
              e.textContent,
              getComputedStyle(e).visibility,
              e.style.translate,
            ])
          )
        );
      }
      return new Set(samples).size === 1;
    });
    expect(stable).toBe(true);
  });
test('省设备数量遵循真实与演示筛选，详细项目仍可查看', async ({ page }) => {
  await load(page);
  const sum = () =>
    page
      .locator('.province-summary')
      .evaluateAll((es) =>
        es.reduce((s, e) => s + Number((e as HTMLElement).dataset.deviceCount), 0)
      );
  await page.getByRole('button', { name: '匿名登记项目', exact: true }).click();
  await expect.poll(sum).toBe(10);
  await page.getByRole('button', { name: '演示设备', exact: true }).click();
  await expect.poll(sum).toBe(44);
  await page.getByRole('button', { name: '全部展示', exact: true }).click();
  await expect.poll(sum).toBe(54);
  await page.locator('.plan-item').filter({ hasText: 'Project-F' }).click();
  await expect(page.getByTestId('project-panel')).toContainText('Project-F');
});
