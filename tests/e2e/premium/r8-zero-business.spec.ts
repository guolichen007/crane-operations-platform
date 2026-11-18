// R8 zero-business platform in a real browser: the server holds NO project, asset, device, alarm,
// resource or schedule (explicit empty Inventory/Fleet after the crane-dev reset). Every page must
// show an honest empty state — never an error banner, never demo data, never a fabricated count —
// and the portfolio entry stays usable so the first project can be registered from the web.
import { test, expect, type Page } from '@playwright/test';
import { routeSummary } from '../summary-mock';
import { summary } from '../../../apps/web-console/tests/summary-fixtures';

const session = (projects: unknown[] = []) => ({ userId: 'u-admin', displayName: '管理员', projects, csrfToken: 'x'.repeat(32), expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' });

async function emptyPlatform(page: Page) {
  const seen = new Set<string>();
  await page.route('**/auth/session', (r) => r.fulfill({ json: session() }));
  await page.route('**/api/v1/**', (r) => {
    const p = new URL(r.request().url()).pathname.replace('/api/v1/', '');
    seen.add(`${r.request().method()} ${p}`);
    if (p === 'portfolio/scopes') return r.fulfill({ json: [{ id: 'PS-ZERO', tenantId: 'demo', code: 'PS-ZERO', name: '零起点组合', status: 'ACTIVE', version: 1, myRole: 'portfolio_manager' }] });
    if (p === 'equipment/types') return r.fulfill({ json: [] });
    if (r.request().method() !== 'GET') return r.fulfill({ status: 405, json: { error: 'unexpected write' } });
    return r.fulfill({ json: [] });
  });
  await routeSummary(page, () => summary({ registered: 0, authorizedProjects: 0, projects: 0, noRuntime: 0, unlocated: 0 }));
  return seen;
}

const noError = async (page: Page) => {
  await expect(page.locator('[data-testid$="-error"], .workspace-error')).toHaveCount(0);
  const hits = page.getByText(/Project-A|P00\d|SIM-0\d\d|演示|DEMO/);
  if (await hits.count()) console.log('DEMO-HIT', page.url(), await hits.first().evaluate((el) => el.outerHTML.slice(0, 300)));
  await expect(hits).toHaveCount(0);
};

test('零业务：首页、运行状态、资产、位置、目录均为空态，无错误、无示例数据、统计为 0', async ({ page }) => {
  await emptyPlatform(page);
  await page.goto('/#/overview?view=2d');
  await expect(page.getByTestId('home-empty')).toBeVisible();
  await expect(page.locator('[data-kpi=registered] .home-kpi-value')).toHaveText('0台');
  await expect(page.locator('[data-kpi=real-connected] .home-kpi-value')).toHaveText('0台');
  await noError(page);
  for (const route of ['/#/runtime', '/#/assets', '/#/locations', '/#/catalog']) {
    await page.goto(route);
    await page.waitForLoadState('networkidle');
    await noError(page);
    await expect(page.locator('tr.data-row')).toHaveCount(0);
  }
  await page.screenshot({ path: `${process.env.R6_EVIDENCE_DIR ?? 'test-results'}/r8-zero-catalog.png` });
});

test('零业务：项目组合各页为空态，新建项目登记与资源/排期入口可用（不发生任何写请求）', async ({ page }) => {
  const seen = await emptyPlatform(page);
  await page.goto('/#/portfolio');
  await expect(page.getByTestId('intake-empty')).toBeVisible();
  await expect(page.getByTestId('intake-create')).toBeVisible();
  const tab = (name: string) => page.locator('.tabs').getByRole('button', { name, exact: true });
  await tab('正式项目').click(); await expect(page.getByTestId('formal-empty')).toBeVisible();
  await tab('资源').click(); await expect(page.getByTestId('resource-empty')).toBeVisible(); await expect(page.getByTestId('resource-create')).toBeVisible();
  await tab('排期').click(); await expect(page.getByTestId('schedule-empty')).toBeVisible(); await expect(page.getByTestId('schedule-create')).toBeVisible();
  await tab('冲突').click(); await expect(page.getByTestId('conflict-empty')).toBeVisible();
  await tab('排期历史').click(); await expect(page.getByTestId('baseline-empty')).toBeVisible();
  await noError(page);
  await tab('项目登记').click();
  await page.getByTestId('intake-create').click();
  await expect(page.getByTestId('intake-create-form')).toBeVisible();
  await expect(page.getByTestId('intake-create-form').getByLabel('项目组合')).toBeVisible();
  expect([...seen].filter((s) => !s.startsWith('GET '))).toEqual([]);
  await page.screenshot({ path: `${process.env.R6_EVIDENCE_DIR ?? 'test-results'}/r8-zero-portfolio.png` });
});
