import { test, expect, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { cloudDevice } from '../../../apps/web-console/tests/cloud-fixtures';
const example = JSON.parse(readFileSync('deploy/inventory/inventory.example.yaml', 'utf8'));
async function inventory(page: Page, geography = false) {
  const projects = example.projects.map((p: Record<string, unknown>) => ({
    ...p,
    assetDeviceCount: example.assetDevices.filter(
      (a: { projectId: string }) => a.projectId === p.id
    ).length,
    runtimeDeviceCount: example.runtimeDevices.filter(
      (d: { projectId: string }) => d.projectId === p.id
    ).length,
    realConnectedCount: 0,
    simulatedCount: example.runtimeDevices.filter(
      (d: { projectId: string }) => d.projectId === p.id
    ).length,
  }));
  if (geography) {
    // Synthetic administrative fixtures deliberately differ from private inventory.
    projects[0].adminCode = 110000;
    projects[6].adminCode = 310000;
  }
  const assets = example.assetDevices.map((a: Record<string, unknown>, i: number) => ({
    ...a,
    runtimeSource: 'SIMULATION',
    runtimeDeviceId: example.runtimeDevices[i].runtimeDeviceId,
    connectivity: 'MQTT_CONNECTED',
  }));
  const devices = example.runtimeDevices.map(
    (r: { runtimeDeviceId: string; projectId: string }) => ({
      ...cloudDevice(r.runtimeDeviceId),
      projectId: r.projectId,
    })
  );
  await page.route('**/api/v1/**', (route) => {
    const path = new URL(route.request().url()).pathname;
    const result =
      path === '/api/v1/projects'
        ? projects
        : path === '/api/v1/assets/devices'
          ? assets
          : path === '/api/v1/devices'
            ? devices
            : path === '/api/v1/alarms'
              ? []
              : devices.find((d: { deviceId: string }) => path.includes(d.deviceId));
    return route.fulfill({ status: result ? 200 : 404, json: result ?? {} });
  });
}
test('Inventory homepage is 9 projects / 10 planned assets / 10 simulated online / 0 real', async ({
  page,
}) => {
  await inventory(page);
  await page.goto('/#/overview');
  await expect(page.getByTestId('inventory-summary').locator('strong')).toHaveText([
    '9',
    '6',
    '3',
    '10',
    '10',
    '0',
  ]);
  await expect(page.getByTestId('cloud-runtime')).toContainText(
    '模拟运行源在线 10 · 真实运行源在线 0'
  );
  await expect(page.locator('.scene-loading')).toBeHidden();
  await expect(page.locator('.inventory-project-marker')).toHaveCount(0);
  await page.locator('[data-project-id="P007"]').click();
  await expect(page.getByTestId('inventory-selection')).toContainText(
    '已完成 · 待接入Cloud · 设备档案待补充'
  );
  await expect(page.getByTestId('inventory-selection')).not.toContainText('OFFLINE');
});
test('only configured administrative centers become project markers, with lifecycle distinctions', async ({
  page,
}) => {
  await inventory(page, true);
  await page.goto('/#/overview');
  await expect(page.locator('.scene-loading')).toBeHidden();
  await expect(page.locator('.inventory-project-marker')).toHaveCount(2);
  await expect(page.locator('.inventory-project-marker.planned')).toHaveCount(1);
  await expect(page.locator('.inventory-project-marker.awaiting')).toHaveCount(1);
  await expect(page.locator('.inventory-project-marker').first()).toHaveAttribute(
    'title',
    '行政区级定位 · 非现场精确坐标'
  );
});
test('three completed projects have no invented device, model or telemetry', async ({ page }) => {
  await inventory(page);
  await page.goto('/#/projects');
  await expect(page.getByTestId('inventory-project')).toHaveCount(9);
  const completed = page.getByTestId('inventory-project').filter({ hasText: '已完成' });
  await expect(completed).toHaveCount(3);
  for (const card of await completed.all()) {
    await expect(card).toContainText('设备档案待补充');
    await expect(card).not.toContainText('OFFLINE');
  }
  await page.goto('/#/devices?project=P007');
  await expect(page.getByText('设备档案待补充 · 尚未接入Cloud；不代表离线故障。')).toBeVisible();
  await expect(page.getByTestId('inventory-asset')).toHaveCount(0);
  await page.goto('/#/devices');
  await expect(page.getByTestId('inventory-asset')).toHaveCount(10);
  await expect(page.getByTestId('inventory-asset').first()).toContainText('规划模拟 · 模拟在线');
  await expect(page.getByTestId('inventory-asset').first()).toContainText('型号：待补充');
  await page.getByTestId('inventory-asset').first().getByRole('link').click();
  await expect(page.getByTestId('device-shadow')).toBeVisible();
  await expect(page.locator('.cloud-devices')).toContainText('Project-A · Crane 1');
});
