import { test } from '@playwright/test';
for (const [w, h] of [[2560, 1440], [1920, 1080], [1440, 900], [1280, 720]] as const) {
  test(`B4 visual ${w}x${h}`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: h });
    await page.goto('/');
    await page.waitForSelector('.province-summary');
    await page.waitForFunction(() => !window.__spatialDiagnostics!().intro.active, null, { timeout: 20000 });
    await page.waitForTimeout(800);
    await page.screenshot({ path: `test-results-v033/regression/b4-country-${w}x${h}.png` });
    await page.locator('.scene-mode-dock').getByRole('button', { name: '资产' }).click();
    await page.waitForTimeout(1200);
    await page.screenshot({ path: `test-results-v033/regression/b4-fleet-${w}x${h}.png` });
    await page.locator('.scene-mode-dock').getByRole('button', { name: '告警' }).click();
    await page.waitForTimeout(800);
    await page.screenshot({ path: `test-results-v033/regression/b4-alarm-${w}x${h}.png` });
  });
}
