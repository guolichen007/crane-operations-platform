import { test, expect, type Page } from '@playwright/test';
import type {
  InventoryProject,
  AssetDevice,
  Shadow,
} from '../../../contracts/typescript/types.gen';
import { cloudDevice } from '../../../apps/web-console/tests/cloud-fixtures';

const projects: InventoryProject[] = [
  {
    id: 'P001',
    name: 'Project-A',
    lifecycle: 'COMPLETED',
    province: '浙江省',
    city: '嘉兴市',
    county: '',
    locationLabel: '浙江嘉兴',
    adminCode: 330400,
    scheduleText: '已完成 · 计划接入',
    notes: '',
    assetDeviceCount: 2,
    runtimeDeviceCount: 1,
    realConnectedCount: 0,
    simulatedCount: 1,
  },
  {
    id: 'P002',
    name: 'Project-B',
    lifecycle: 'PLANNED',
    province: '江苏省',
    city: '南京市',
    county: '',
    locationLabel: '江苏南京',
    adminCode: 320100,
    scheduleText: '计划实施',
    notes: '',
    assetDeviceCount: 1,
    runtimeDeviceCount: 0,
    realConnectedCount: 0,
    simulatedCount: 0,
  },
];

const assets: AssetDevice[] = [
  {
    assetDeviceId: 'A1',
    projectId: 'P001',
    displayName: '行车 A1',
    craneModel: 'QZ10t',
    ratedCapacityT: 10,
    spanM: 25,
    workClass: 'A8',
    grabType: '机械双瓣抓斗',
    lifecycle: 'COMPLETED',
    runtimeSource: 'SIMULATION',
    runtimeDeviceId: 'R1',
    connectivity: 'MQTT_CONNECTED',
  },
  {
    assetDeviceId: 'A2',
    projectId: 'P001',
    displayName: '行车 A2',
    craneModel: 'QZ10t',
    ratedCapacityT: 10,
    spanM: 25,
    workClass: 'A8',
    grabType: '机械双瓣抓斗',
    lifecycle: 'COMPLETED',
    runtimeSource: null,
    runtimeDeviceId: null,
    connectivity: null,
  },
  {
    assetDeviceId: 'B1',
    projectId: 'P002',
    displayName: '行车 B1',
    craneModel: 'QZ16t',
    ratedCapacityT: 16,
    spanM: 28,
    workClass: 'A8',
    grabType: '液压多瓣抓斗',
    lifecycle: 'PLANNED',
    runtimeSource: null,
    runtimeDeviceId: null,
    connectivity: null,
  },
];

const shadows: Shadow[] = [cloudDevice('R1')];

async function mockApi(page: Page) {
  await page.route('**/api/v1/**', (route) => {
    const path = new URL(route.request().url()).pathname;
    const result =
      path === '/api/v1/projects'
        ? projects
        : path === '/api/v1/assets/devices'
          ? assets
          : path === '/api/v1/devices'
            ? shadows
            : path === '/api/v1/alarms'
              ? []
              : shadows.find((s) => path.includes(s.deviceId)) ?? {};
    return route.fulfill({ status: result ? 200 : 404, json: result ?? {} });
  });
}

test('跨路由从 /assets 全局搜索真实资产并深链定位', async ({ page }) => {
  await mockApi(page);
  await page.goto('/#/assets');
  await expect(page.getByTestId('asset-center')).toBeVisible();
  await page.keyboard.press('Control+K');
  await expect(page.locator('.global-search')).toBeVisible();
  await page.getByLabel('搜索资产、项目或省市').fill('A1');
  await page.locator('.global-search-item[data-result-kind="device"]').first().click();
  // Canonical deep link, not event-before-navigation.
  await expect(page).toHaveURL(/#\/overview\?asset=A1/);
  await expect(page.locator('.scene-mode-dock')).toBeVisible();
  await expect(page.getByTestId('device-panel')).toHaveAttribute('data-device-id', 'A1');
});

test('跨路由从 /portfolio 全局搜索项目并深链定位', async ({ page }) => {
  await mockApi(page);
  await page.goto('/#/portfolio');
  await expect(page.getByTestId('portfolio-workspace')).toBeVisible();
  await page.keyboard.press('Control+K');
  await page.getByLabel('搜索资产、项目或省市').fill('Project-B');
  await page.locator('.global-search-item[data-result-kind="project"]').first().click();
  await expect(page).toHaveURL(/#\/overview\?project=P002/);
  await expect(page.locator('.scene-mode-dock')).toBeVisible();
  await expect(page.getByTestId('project-panel')).toHaveAttribute('data-project-id', 'P002');
});

test('深链连续切换：同页面 asset A→B 不刷新也生效', async ({ page }) => {
  await mockApi(page);
  await page.goto('/#/overview?asset=A1');
  await expect(page.getByTestId('device-panel')).toHaveAttribute('data-device-id', 'A1');
  // Same-document hash navigation (no full reload) must re-run the deep link.
  await page.goto('/#/overview?asset=A2');
  await expect(page).toHaveURL(/#\/overview\?asset=A2/);
  await expect(page.getByTestId('device-panel')).toHaveAttribute('data-device-id', 'A2');
});

test('无遥测资产不伪造 0/自动/闭合/无故障', async ({ page }) => {
  await mockApi(page);
  await page.goto('/#/overview?asset=A2');
  const panel = page.getByTestId('device-panel');
  await expect(panel).toHaveAttribute('data-device-id', 'A2');
  await expect(panel).toContainText('NO TELEMETRY');
  await expect(panel).toContainText('未接入运行数据');
  // 未知数据必须显示占位，不得伪造现场事实。
  await expect(panel).not.toContainText('无活动故障');
  await expect(panel).not.toContainText('0.00 m');
  await expect(panel).not.toContainText('闭合');
});
