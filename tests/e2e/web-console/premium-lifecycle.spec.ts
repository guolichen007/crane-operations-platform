import { test, expect } from '@playwright/test';
import { writeFileSync } from 'node:fs';

test('Premium 20 次全国项目设备聚焦往返保持 GPU 内存有界', async ({ page }) => {
  test.setTimeout(240000);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto('/');
  await expect(page.locator('.province-summary')).toHaveCount(34);
  await expect(page.locator('.scene-loading')).toBeHidden();
  const memory: { geometries: number; textures: number }[] = [];
  for (let i = 0; i < 20; i++) {
    await page.locator('.plan-item').filter({ hasText: 'Project-A' }).click();
    await expect(page.getByTestId('project-panel')).toBeVisible();
    await expect(page.locator('main')).toHaveAttribute('data-scene-state', 'PROJECT_FOCUS');
    await page.getByTestId('project-panel').locator('[data-device-id]').first().click();
    await expect(page.locator('main')).toHaveAttribute('data-camera-preset', 'DEVICE_FOCUS');
    await expect(page.locator('.twin-canvas canvas')).toHaveCount(1);
    await page.getByRole('button', { name: '关闭详情' }).click();
    await expect(page.locator('.twin-canvas canvas')).toHaveCount(0);
    await page.getByRole('button', { name: '重置全国视角' }).click();
    await expect(page.locator('main')).toHaveAttribute('data-scene-state', 'COUNTRY_IDLE');
    await page.waitForTimeout(80);
    memory.push(
      await page.evaluate(
        () =>
          (
            window.__spatialDiagnostics!() as unknown as {
              memory: { geometries: number; textures: number };
            }
          ).memory
      )
    );
  }
  expect(Math.max(...memory.slice(-5).map((m) => m.geometries))).toBeLessThanOrEqual(
    Math.max(...memory.slice(0, 5).map((m) => m.geometries)) + 8
  );
  expect(Math.max(...memory.slice(-5).map((m) => m.textures))).toBeLessThanOrEqual(
    Math.max(...memory.slice(0, 5).map((m) => m.textures)) + 2
  );
  writeFileSync(
    test.info().outputPath('national-project-device-memory.json'),
    JSON.stringify(memory)
  );
  await test.info().attach('national-project-device-memory', {
    body: JSON.stringify(memory),
    contentType: 'application/json',
  });
});
