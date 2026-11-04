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
  await page.evaluate(() => {
    (window as unknown as { __noReload?: boolean }).__noReload = true;
  });
  await page.goto('/#/overview?asset=A2');
  // Wait for the router (not just the browser URL) to reflect the new query.
  await page.waitForFunction(() => {
    const q = (window as unknown as { __routeQuery?: () => Record<string, unknown> }).__routeQuery?.();
    return q?.asset === 'A2';
  });
  expect(await page.evaluate(() => (window as unknown as { __noReload?: boolean }).__noReload)).toBe(
    true
  );
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

test('API 城市项目按省市区逐级钻取保持可见，且不跨省泄漏', async ({ page }) => {
  await mockApi(page);
  await page.goto('/#/overview');
  await expect(page.locator('.province-summary')).toHaveCount(34);
  await expect(page.locator('.scene-loading')).toBeHidden();
  await page.waitForFunction(() => !window.__spatialDiagnostics!().intro.active);
  const clusterCoord = (id: string) =>
    page.evaluate((i) => {
      const p = window.__spatialDiagnostics!().picks.find((x) => x.kind === 'cluster' && x.id === i);
      return p ? [p.x, p.y] : null;
    }, id);
  // 全国：嘉兴项目（P001, adminCode 330400）参与浙江省聚合。
  await expect
    .poll(() =>
      page.evaluate(() =>
        window.__spatialDiagnostics!().picks.some((p) => p.kind === 'cluster' && p.id === '330000')
      )
    )
    .toBe(true);
  // 进入浙江（地图拾取坐标）→ P001 项目标签仍存在（省级钻取不得过滤城市项目）。
  const zj = await clusterCoord('330000');
  expect(zj).not.toBeNull();
  await page.mouse.click(zj![0], zj![1]);
  await expect(page.locator('main')).toHaveAttribute('data-adcode', '330000');
  await expect(page.locator('.map-project.real', { hasText: 'Project-A' })).toHaveCount(1);
  // 进入嘉兴（点击地图嘉兴市区域标签）→ P001 仍可见。
  await page.locator('.map-region', { hasText: '嘉兴' }).click();
  await expect(page.locator('main')).toHaveAttribute('data-adcode', '330400');
  await expect(page.locator('.map-project.real', { hasText: 'Project-A' })).toHaveCount(1);
  // 返回全国 → 进入江苏 → P001 不出现，P002 出现。
  await page.getByRole('button', { name: '重置全国视角' }).click();
  await expect(page.locator('main')).toHaveAttribute('data-adcode', '100000');
  const js = await clusterCoord('320000');
  expect(js).not.toBeNull();
  await page.mouse.click(js![0], js![1]);
  await expect(page.locator('main')).toHaveAttribute('data-adcode', '320000');
  await expect(page.locator('.map-project.real', { hasText: 'Project-B' })).toHaveCount(1);
  await expect(page.locator('.map-project.real', { hasText: 'Project-A' })).toHaveCount(0);
});
