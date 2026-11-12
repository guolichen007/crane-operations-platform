// Q4-QA-R1 §5: two light UI fixes, real browser against a mocked API (no server writes).
//  - CatalogCenter: error / loading / empty / success are distinct; a 503 is never "暂无目录"; retry works.
//  - Conflict override: the button follows CapScheduleOverride (portfolio_manager on the owning scope)
//    and the hard-conflict rule; the server still decides (portfolio_security_test / scheduling tests).
import { test, expect, type Page } from '@playwright/test';

const session = (role = 'platform_admin') => ({ userId: 'u', displayName: 'u', projects: [{ projectId: 'P001', projectName: 'Project-A', role }], csrfToken: 'x'.repeat(32), expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' });

async function catalogMock(page: Page, fail: { manufacturers?: number; catalog?: number }) {
  const state = { fail: { ...fail }, calls: 0 };
  await page.route('**/auth/session', (r) => r.fulfill({ json: session() }));
  await page.route('**/api/v1/**', (r) => {
    const p = new URL(r.request().url()).pathname;
    if (p === '/api/v1/manufacturers') {
      state.calls++;
      if (state.fail.manufacturers) return r.fulfill({ status: state.fail.manufacturers, json: { error: 'database unavailable' } });
      return r.fulfill({ json: [{ id: 'M1', projectId: null, name: '一号厂', shortName: '一号', countryRegion: 'CN', status: 'ACTIVE', version: 1 }] });
    }
    if (p === '/api/v1/equipment/catalog') {
      if (state.fail.catalog) return r.fulfill({ status: state.fail.catalog, json: { error: 'database unavailable' } });
      return r.fulfill({ json: [] });
    }
    return r.fulfill({ json: [] });
  });
  return state;
}

test('设备目录：503 显示错误与重试（不显示“暂无”），重试成功后显示数据；空目录显示空态', async ({ page }) => {
  const state = await catalogMock(page, { manufacturers: 503, catalog: 503 });
  await page.goto('/#/catalog');
  await expect(page.getByTestId('manufacturer-error')).toContainText('服务暂不可用');
  await expect(page.getByTestId('manufacturer-empty')).toHaveCount(0);
  await page.screenshot({ path: 'test-results/q4-catalog/catalog-503.png' });
  state.fail.manufacturers = 0;
  await page.getByTestId('manufacturer-error').getByRole('button', { name: '重试' }).click();
  await expect(page.getByTestId('manufacturer-table')).toContainText('一号厂');
  await page.getByRole('button', { name: '型号目录' }).click();
  await expect(page.getByTestId('catalog-error')).toBeVisible();
  await expect(page.getByTestId('catalog-empty')).toHaveCount(0);
  state.fail.catalog = 0;
  await page.getByTestId('catalog-error').getByRole('button', { name: '重试' }).click();
  await expect(page.getByTestId('catalog-empty')).toContainText('暂无型号目录');
});

type Scope = { id: string; myRole: 'portfolio_planner' | 'portfolio_manager' | null };
async function conflictMock(page: Page, scopes: Scope[], overrideAllowed = true) {
  const posts: string[] = [];
  await page.route('**/auth/session', (r) => r.fulfill({ json: session('viewer') }));
  await page.route('**/api/v1/**', (r) => {
    const p = new URL(r.request().url()).pathname.replace('/api/v1/', '');
    const m = r.request().method();
    if (p === 'portfolio/scopes') return r.fulfill({ json: scopes.map((s) => ({ id: s.id, tenantId: 'demo', code: s.id, name: s.id, status: 'ACTIVE', version: 1, myRole: s.myRole })) });
    if (p === 'portfolio/resources') return r.fulfill({ json: [{ id: 'R1', portfolioScopeId: scopes[0].id, resourceType: 'TEAM', referenceId: null, name: '安装一队', capacity: 1, exclusive: true, overrideAllowed, calendarId: null, status: 'ACTIVE', version: 1 }] });
    if (p === 'portfolio/conflicts') return r.fulfill({ json: [{ id: 7, conflictType: 'RESOURCE_OVERLAP', severity: 'WARNING', leftItemId: 'S1', rightItemId: 'S2', resourceId: 'R1', status: 'OPEN' }] });
    if (p === 'portfolio/conflicts/7/override' && m === 'POST') { posts.push(p); return r.fulfill({ json: { status: 'overridden' } }); }
    return r.fulfill({ json: [] });
  });
  return posts;
}
async function openConflicts(page: Page) {
  await page.goto('/#/portfolio');
  await page.getByRole('tab', { name: /冲突/ }).or(page.getByRole('button', { name: /冲突/ })).first().click();
  await expect(page.getByTestId('conflict-table')).toBeVisible();
}

test('冲突覆盖：项目组合规划员看不到可执行的覆盖按钮', async ({ page }) => {
  await conflictMock(page, [{ id: 'PS-A', myRole: 'portfolio_planner' }]);
  await openConflicts(page);
  await expect(page.getByTestId('conflict-override')).toHaveCount(0);
  await expect(page.getByTestId('override-not-allowed')).toContainText('需项目组合经理');
  await page.screenshot({ path: 'test-results/q4-catalog/override-planner.png' });
});

test('冲突覆盖：项目组合经理可覆盖（发送一次请求）；资源不允许覆盖的硬冲突不显示按钮', async ({ page }) => {
  const posts = await conflictMock(page, [{ id: 'PS-A', myRole: 'portfolio_manager' }]);
  await openConflicts(page);
  page.once('dialog', (d) => void d.accept('资源排期经确认调整'));
  await page.getByTestId('conflict-override').click();
  await expect.poll(() => posts.length).toBe(1);

  const hard = await page.context().newPage();
  await conflictMock(hard, [{ id: 'PS-A', myRole: 'portfolio_manager' }], false);
  await openConflicts(hard);
  await expect(hard.getByTestId('conflict-override')).toHaveCount(0);
  await expect(hard.getByTestId('override-hard')).toContainText('不可覆盖');
});

test('冲突覆盖：同时是一个组合的规划员和另一个组合的经理——不显示可执行按钮（冲突契约无范围字段）', async ({ page }) => {
  await conflictMock(page, [{ id: 'PS-A', myRole: 'portfolio_manager' }, { id: 'PS-B', myRole: 'portfolio_planner' }]);
  await openConflicts(page);
  await expect(page.getByTestId('conflict-override')).toHaveCount(0);
});
