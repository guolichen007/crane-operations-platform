// Q4-QA-R1a §4: PortfolioGrant (portfolio_memberships) is a separate authorization system from the
// ProjectGrants in WebSession.projects, so a portfolio revocation / downgrade does NOT change the web
// session. After the browser re-reads the portfolio scope (a 403 from the server, or re-entering the
// page), data of a portfolio the caller lost must not stay visible, and capabilities of a lost role
// (approve / override) must not stay offered. Mock follows the server: every list is filtered by the
// CURRENT membership and every write re-checks it.
import { test, expect, type Page } from '@playwright/test';

type Role = 'portfolio_manager' | 'portfolio_planner' | null;
const session = { userId: 'u', displayName: 'pm', projects: [], csrfToken: 'x'.repeat(32), expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' };

async function mock(page: Page) {
  const state = { role: 'portfolio_manager' as Role, writes: [] as string[] };
  const intake = { id: 'I1', portfolioScopeId: 'PS-A', tenantId: 'demo', intakeCode: 'PA-001', projectName: '甲组合项目', customerName: '', projectType: 'NEW_INSTALLATION', priority: 'MEDIUM', riskLevel: 'LOW', scopeSummary: '', siteReference: '', registeredAt: '2026-11-01T00:00:00Z', registeredBy: 'u', requestedStart: null, requestedHandover: null, plannedStart: null, plannedEnd: null, status: 'PLANNING', formalProjectId: null, projectManagerUserId: null, technicalLeadUserId: null, siteLeadUserId: null, version: 3 };
  const member = () => state.role !== null;
  await page.route('**/auth/session', (r) => r.fulfill({ json: session }));
  await page.route('**/api/v1/**', async (r) => {
    const p = new URL(r.request().url()).pathname.replace('/api/v1/', '');
    const m = r.request().method();
    if (m !== 'GET') {
      state.writes.push(`${m} ${p}`);
      const needsManager = p.endsWith('/approve') || p.endsWith('/override');
      if (!member() || (needsManager && state.role !== 'portfolio_manager')) return r.fulfill({ status: 403, json: { error: 'Forbidden' } });
      return r.fulfill({ json: { status: 'ok' } });
    }
    if (p === 'portfolio/scopes') return r.fulfill({ json: member() ? [{ id: 'PS-A', tenantId: 'demo', code: 'PS-A', name: '甲项目组合', status: 'ACTIVE', version: 1, myRole: state.role }] : [] });
    if (p === 'portfolio/intakes') return r.fulfill({ json: member() ? [intake] : [] });
    if (p === 'portfolio/resources') return r.fulfill({ json: member() ? [{ id: 'R1', portfolioScopeId: 'PS-A', resourceType: 'TEAM', referenceId: null, name: '甲安装队', capacity: 1, exclusive: true, overrideAllowed: true, calendarId: null, status: 'ACTIVE', version: 1 }] : [] });
    if (p === 'portfolio/conflicts') return r.fulfill({ json: member() ? [{ id: 7, conflictType: 'RESOURCE_OVERLAP', severity: 'WARNING', leftItemId: 'S1', rightItemId: 'S2', resourceId: 'R1', status: 'OPEN' }] : [] });
    if (p === 'portfolio/projects') return r.fulfill({ json: [] });
    return r.fulfill({ json: [] });
  });
  return state;
}
const tabButton = (page: Page, name: string) => page.locator('.tabs').getByRole('button', { name, exact: true });

test('组合授权被撤销：一次写操作得到 403 后重新读取，甲组合的登记、资源、冲突与新建入口全部消失', async ({ page }) => {
  const state = await mock(page);
  page.on('dialog', (d) => void d.accept());
  await page.goto('/#/portfolio');
  await expect(page.locator('tr:has-text("PA-001")')).toBeVisible();
  await tabButton(page, '资源').click();
  await expect(page.getByText('甲安装队')).toBeVisible();
  await tabButton(page, '项目登记').click();
  state.role = null; // revoked server-side; WebSession (ProjectGrants) unchanged
  await page.getByRole('button', { name: /批准/ }).first().click();
  await expect(page.getByRole('alert').first()).toBeVisible();
  // Re-read after the 403: nothing of the lost portfolio stays on screen.
  await expect(page.locator('tr:has-text("PA-001")')).toHaveCount(0);
  await expect(page.getByTestId('intake-create')).toHaveCount(0);
  await tabButton(page, '资源').click();
  await expect(page.getByText('甲安装队')).toHaveCount(0);
  await tabButton(page, '冲突').click();
  await expect(page.getByTestId('conflict-table')).toHaveCount(0);
  await page.screenshot({ path: 'test-results/q4a/portfolio-revoked.png' });
});

test('经理降级为规划员：批准得到 403 后，批准与覆盖按钮不再提供', async ({ page }) => {
  const state = await mock(page);
  page.on('dialog', (d) => void d.accept());
  await page.goto('/#/portfolio');
  await expect(page.getByRole('button', { name: /批准/ }).first()).toBeVisible();
  state.role = 'portfolio_planner';
  await page.getByRole('button', { name: /批准/ }).first().click();
  await expect(page.getByRole('alert').first()).toBeVisible();
  await expect(page.getByRole('button', { name: /批准/ })).toHaveCount(0);
  await tabButton(page, '冲突').click();
  await expect(page.getByTestId('conflict-table')).toBeVisible();
  await expect(page.getByTestId('conflict-override')).toHaveCount(0);
});

test('撤销后离开并重新进入项目组合页（重新读取授权范围）：不显示缓存中的甲组合数据', async ({ page }) => {
  const state = await mock(page);
  await page.goto('/#/portfolio');
  await expect(page.locator('tr:has-text("PA-001")')).toBeVisible();
  state.role = null;
  await page.evaluate(() => (location.hash = '#/assets'));
  await expect(page.getByTestId('asset-center')).toBeVisible();
  await page.evaluate(() => (location.hash = '#/portfolio'));
  await expect(page.getByTestId('portfolio-workspace')).toBeVisible();
  await page.waitForTimeout(1500);
  await expect(page.locator('tr:has-text("PA-001")')).toHaveCount(0);
  await expect(page.getByTestId('intake-create')).toHaveCount(0);
});
