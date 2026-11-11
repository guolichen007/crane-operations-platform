import { test, expect } from '@playwright/test';
import { login, sessionCsrf } from './login';
import { code, assertWriteTarget } from './run';

// Q1 project registration on the real stack (Keycloak Code+PKCE, BFF session/CSRF,
// PostgreSQL). Fixture: PS-B4 portfolio; b4-viewer = portfolio_planner (register, no approve),
// b4-maintainer = portfolio_manager (approve), b4-noauth = no portfolio membership.
// Every intake code is run-unique, so persistent environments never collide.

test('项目登记：规划者在浏览器登记并推进，经理批准得到正式项目，且不自动授权、不进地图', async ({ browser }) => {
  const intakeCode = code('INT', 'Q1');
  const name = `Q1 真实登记 ${intakeCode}`;

  const planner = await (await browser.newContext()).newPage();
  await login(planner, 'b4-viewer');
  await assertWriteTarget(planner);
  await planner.goto('/#/portfolio');
  await planner.getByTestId('intake-create').click();
  const form = planner.getByTestId('intake-create-form');
  await form.getByLabel('项目组合').selectOption('PS-B4');
  await form.getByLabel('登记编号').fill(intakeCode);
  await form.getByLabel('项目类型').fill('NEW_INSTALLATION');
  await form.getByLabel('项目名称').fill(name);
  await form.getByRole('button', { name: '保存登记' }).click();
  await expect(form).toBeHidden();
  await planner.reload();
  const row = planner.locator('tr.data-row', { hasText: intakeCode });
  await expect(row).toContainText('已登记');
  await row.getByTestId('intake-advance').click();
  await expect(row).toContainText('评审中');
  await row.getByTestId('intake-advance').click();
  await expect(row).toContainText('规划中');
  // Planner cannot approve: no approve button (Q2), and the server still answers 403.
  await expect(row.getByTestId('intake-approve')).toHaveCount(0);
  await expect(row.getByTestId('await-approval')).toBeVisible();
  const intakeId = await row.getAttribute('data-intake-id');
  const approveStatus = await planner.evaluate(async ({ id, csrf }) => (await fetch(`/api/v1/portfolio/intakes/${id}/approve`, {
    method: 'POST', headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' }, body: '{}',
  })).status, { id: intakeId!, csrf: await sessionCsrf(planner) });
  expect(approveStatus).toBe(403);
  await expect(row).toContainText('规划中');
  await planner.screenshot({ path: 'test-results/b4-visual/q1-planner-cannot-approve.png' });

  const manager = await (await browser.newContext()).newPage();
  await login(manager, 'b4-maintainer');
  await assertWriteTarget(manager);
  await manager.goto('/#/portfolio');
  const mrow = manager.locator('tr', { hasText: intakeCode });
  manager.once('dialog', (d) => void d.accept());
  await mrow.getByTestId('intake-approve').click();
  const notice = manager.getByTestId('portfolio-notice');
  await expect(notice).toContainText('已批准并生成正式项目');
  const projectId = /正式项目 ([A-Za-z0-9_-]+)/.exec((await notice.textContent()) ?? '')?.[1];
  expect(projectId, 'approval returns a formal project id').toBeTruthy();
  await expect(mrow).toContainText('已批准');
  await expect(mrow.getByTestId('intake-stages')).toContainText('尚未获得该项目的访问');
  await manager.screenshot({ path: 'test-results/b4-visual/q1-manager-approved.png' });

  // No auto-grant: neither the approver nor the registrant sees the formal project as an
  // authorized / Inventory (map) project, and neither can register assets in it.
  for (const page of [manager, planner]) {
    const r = await page.evaluate(async ({ id, csrf }) => {
      const projects = await (await fetch('/api/v1/projects')).text();
      const session = await (await fetch('/auth/session')).json();
      const create = await fetch('/api/v1/assets', { method: 'POST', headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' }, body: JSON.stringify({ projectId: id, assetType: 'CRANE', assetCode: 'Q1-NOGRANT' }) });
      return { inInventory: projects.includes(id), granted: (session.projects as { projectId: string }[]).some((p) => p.projectId === id), create: create.status };
    }, { id: projectId!, csrf: await sessionCsrf(page) });
    expect(r.inInventory).toBe(false);
    expect(r.granted).toBe(false);
    expect([403, 404]).toContain(r.create);
  }
});

test('项目登记：无项目组合成员资格的账号看不到入口，服务端拒绝创建', async ({ page }) => {
  await login(page, 'b4-noauth');
  await page.goto('/#/portfolio');
  await expect(page.getByTestId('intake-no-scope')).toBeVisible();
  await expect(page.getByTestId('intake-create')).toHaveCount(0);
  const status = await page.evaluate(async (csrf) => (await fetch('/api/v1/portfolio/intakes', {
    method: 'POST', headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' },
    body: JSON.stringify({ portfolioScopeId: 'PS-B4', intakeCode: 'NOAUTH', projectName: 'x', customerName: null, projectType: 'T', priority: 'MEDIUM', riskLevel: 'MEDIUM', scopeSummary: '', siteReference: null, requestedStart: null, requestedHandover: null, plannedStart: null, plannedEnd: null, projectManagerUserId: null, technicalLeadUserId: null, siteLeadUserId: null }),
  })).status, await sessionCsrf(page));
  expect(status).toBe(403);
});
