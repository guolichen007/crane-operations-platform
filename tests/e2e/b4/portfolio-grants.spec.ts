import { test, expect, type Page } from '@playwright/test';
import { login, sessionCsrf } from './login';
import { code, assertWriteTarget } from './run';

// Q2 on the real stack (Keycloak Code+PKCE, BFF session/CSRF, PostgreSQL):
// register (planner b4-viewer) → approve (manager b4-maintainer) → manager grants b4-grantee
// platform_admin on the formal project in the browser → b4-grantee creates an asset there
// through the asset UI → manager revokes → b4-grantee's OLD session loses the project.
// Negative paths: planner has no grant entry and the server refuses; a project-only
// platform_admin (b4-admin, no portfolio membership) cannot see or manage formal projects.

async function registerAndApprove(planner: Page, manager: Page, intakeCode: string): Promise<string> {
  await planner.goto('/#/portfolio');
  await planner.getByTestId('intake-create').click();
  const form = planner.getByTestId('intake-create-form');
  await form.getByLabel('项目组合').selectOption('PS-B4');
  await form.getByLabel('登记编号').fill(intakeCode);
  await form.getByLabel('项目类型').fill('NEW_INSTALLATION');
  await form.getByLabel('项目名称').fill(`Q2 授权验收 ${intakeCode}`);
  await form.getByRole('button', { name: '保存登记' }).click();
  await expect(form).toBeHidden();
  const row = planner.locator('tr', { hasText: intakeCode });
  await row.getByTestId('intake-advance').click();
  await expect(row).toContainText('评审中');
  await row.getByTestId('intake-advance').click();
  await expect(row).toContainText('规划中');
  await expect(row.getByTestId('intake-approve')).toHaveCount(0);

  await manager.goto('/#/portfolio');
  const mrow = manager.locator('tr', { hasText: intakeCode });
  manager.once('dialog', (d) => void d.accept());
  await mrow.getByTestId('intake-approve').click();
  const notice = manager.getByTestId('portfolio-notice');
  await expect(notice).toContainText('已批准并生成正式项目');
  return /正式项目 ([A-Za-z0-9_-]+)/.exec((await notice.textContent()) ?? '')![1];
}

test('正式项目授权闭环：经理在浏览器授权 → 被授权人登记资产 → 撤销后旧会话失效', async ({ browser }) => {
  const planner = await (await browser.newContext()).newPage();
  const manager = await (await browser.newContext()).newPage();
  const grantee = await (await browser.newContext()).newPage();
  await login(planner, 'b4-viewer');
  await login(manager, 'b4-maintainer');
  await login(grantee, 'b4-grantee');
  for (const p of [planner, manager, grantee]) await assertWriteTarget(p);
  const projectId = await registerAndApprove(planner, manager, code('INT', 'Q2'));

  // Before the grant: the grantee cannot reach the project at all.
  const before = await grantee.evaluate(async (id) => (await fetch(`/api/v1/assets?project=${id}`)).text(), projectId);
  expect(before).not.toContain(projectId);

  // Manager grants in the browser (formal project tab → panel → form).
  await manager.getByTestId('tab-formal').click();
  await manager.locator(`[data-formal-id="${projectId}"]`).getByTestId('formal-open').click();
  const panel = manager.getByTestId('formal-panel');
  await expect(panel.getByTestId('formal-grant-status')).toContainText('尚未配置');
  await panel.getByTestId('grant-form').getByLabel('平台账号 ID').fill('b4-grantee');
  await panel.getByTestId('grant-form').getByLabel('项目角色').selectOption('platform_admin');
  await panel.getByTestId('grant-submit').click();
  await expect(panel.getByTestId('grant-notice')).toContainText('立即生效');
  await expect(panel.locator('[data-member="b4-grantee"]')).toBeVisible();
  await manager.screenshot({ path: 'test-results/b4-visual/q2-manager-granted.png' });

  // Grantee: a fresh page load picks up the new grant; the asset center offers the project
  // and the create succeeds through the real UI.
  await grantee.goto('/#/assets');
  await grantee.reload();
  await grantee.getByTestId('asset-create').click();
  const createForm = grantee.getByTestId('asset-create-form');
  await createForm.getByLabel('授权项目选择').selectOption(projectId);
  const assetCode = code('CRN', 'Q2-GRANTED');
  await createForm.getByPlaceholder('CRN-001').fill(assetCode);
  await createForm.getByRole('button', { name: '创建' }).click();
  const detail = grantee.getByTestId('asset-detail');
  await expect(detail).toBeVisible();
  await expect(detail).toContainText(assetCode);
  await expect(detail).toHaveAttribute('data-project-id', projectId);
  await grantee.screenshot({ path: 'test-results/b4-visual/q2-grantee-asset.png' });

  // Q3: before registration the project is not in Inventory; the grantee (project
  // platform_admin) registers it through the projects page; the SAME id then appears in
  // /projects with the asset counted and no runtime (REAL 0).
  const inventoryBefore = await grantee.evaluate(async () => (await fetch('/api/v1/projects')).text());
  expect(inventoryBefore).not.toContain(projectId);
  // The portfolio manager holds no ProjectGrant: the server refuses the registration.
  const pmRegister = await manager.evaluate(async ({ id, csrf }) => (await fetch(`/api/v1/projects/${id}/inventory-registration`, {
    method: 'POST', headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' },
    body: JSON.stringify({ lifecycle: 'PLANNED', adminCode: 330481, locationLabel: '', scheduleText: '', notes: '' }),
  })).status, { id: projectId, csrf: await sessionCsrf(manager) });
  expect(pmRegister).toBe(404);
  await grantee.goto('/#/projects');
  await grantee.locator(`[data-pending-id="${projectId}"]`).getByTestId('register-open').click();
  const reg = grantee.getByTestId('registration-form');
  await reg.getByLabel('项目生命周期').selectOption('PLANNED');
  await reg.getByLabel('省', { exact: true }).selectOption('330000');
  await reg.getByLabel('市', { exact: true }).selectOption('330400');
  await reg.getByLabel('区县').selectOption('330481');
  await reg.getByTestId('registration-submit').click();
  await expect(reg).toBeHidden();
  const card = grantee.locator(`[data-project-id="${projectId}"]`);
  await expect(card).toContainText('浙江省 嘉兴市 海宁市 · 行政区级定位');
  await expect(card.getByTestId('runtime-source')).toHaveText('尚未接入运行源');
  await grantee.screenshot({ path: 'test-results/b4-visual/q3-grantee-registered.png' });
  const inv = await grantee.evaluate(async (id) => {
    const projects = (await (await fetch('/api/v1/projects')).json()) as { id: string; assetDeviceCount: number; realConnectedCount: number; adminCode: number }[];
    const summary = await (await fetch('/api/v1/operations/summary?region=330481&alarmWindow=PT24H')).json();
    return { p: projects.find((x) => x.id === id), registered: summary.metrics.assets.registered, real: summary.metrics.real.connected };
  }, projectId);
  expect(inv.p).toMatchObject({ assetDeviceCount: 1, realConnectedCount: 0, adminCode: 330481 });
  expect(inv.registered).toBeGreaterThanOrEqual(1);
  expect(inv.real).toBe(0);

  // The manager's portfolio role still gives no project asset access.
  const pmView = await manager.evaluate(async (id) => (await fetch(`/api/v1/assets?project=${id}`)).text(), projectId);
  expect(pmView).not.toContain(assetCode);

  // Revoke in the browser; the grantee's EXISTING session loses access on its next request.
  manager.once('dialog', (d) => void d.accept());
  await panel.locator('[data-member="b4-grantee"]').getByTestId('member-revoke').click();
  await expect(panel.getByTestId('members-empty')).toBeVisible();
  const after = await grantee.evaluate(async ({ id, csrf, c }) => {
    const list = await (await fetch(`/api/v1/assets?project=${id}`)).text();
    const create = await fetch('/api/v1/assets', { method: 'POST', headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' }, body: JSON.stringify({ projectId: id, assetType: 'CRANE', assetCode: c }) });
    const session = await (await fetch('/auth/session')).json();
    return { list, create: create.status, granted: (session.projects as { projectId: string }[]).some((p) => p.projectId === id) };
  }, { id: projectId, csrf: await sessionCsrf(grantee), c: code('CRN', 'Q2-AFTER-REVOKE') });
  expect(after.list).not.toContain(assetCode);
  expect([403, 404]).toContain(after.create);
  expect(after.granted).toBe(false);
});

test('正式项目授权反证：规划者无授权入口且服务端拒绝；仅有项目权限的 platform_admin 不能越权', async ({ browser }) => {
  const planner = await (await browser.newContext()).newPage();
  const manager = await (await browser.newContext()).newPage();
  await login(planner, 'b4-viewer');
  await login(manager, 'b4-maintainer');
  const projectId = await registerAndApprove(planner, manager, code('INT', 'Q2-NEG'));

  // The planner page was loaded before the approval: switching to the formal tab refetches.
  await planner.getByTestId('tab-formal').click();
  await planner.locator(`[data-formal-id="${projectId}"]`).getByTestId('formal-open').click();
  await expect(planner.getByTestId('grant-readonly')).toBeVisible();
  await expect(planner.getByTestId('grant-form')).toHaveCount(0);
  const plannerStatus = await planner.evaluate(async ({ id, csrf }) => (await fetch(`/api/v1/portfolio/projects/${id}/members`, {
    method: 'POST', headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' }, body: JSON.stringify({ userId: 'b4-grantee', role: 'viewer', expectedRole: null }),
  })).status, { id: projectId, csrf: await sessionCsrf(planner) });
  expect(plannerStatus).toBe(403);

  const admin = await (await browser.newContext()).newPage();
  await login(admin, 'b4-admin');
  const r = await admin.evaluate(async ({ id, csrf }) => ({
    list: await (await fetch('/api/v1/portfolio/projects')).text(),
    detail: (await fetch(`/api/v1/portfolio/projects/${id}`)).status,
    grant: (await fetch(`/api/v1/portfolio/projects/${id}/members`, { method: 'POST', headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' }, body: JSON.stringify({ userId: 'b4-admin', role: 'platform_admin', expectedRole: null }) })).status,
  }), { id: projectId, csrf: await sessionCsrf(admin) });
  expect(r.list).not.toContain(projectId);
  expect(r.detail).toBe(404);
  expect(r.grant).toBe(404);
});
