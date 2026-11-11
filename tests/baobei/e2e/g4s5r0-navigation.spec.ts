import { test, expect, action, api, uniq } from './support/isolation';

// G4-S5-R0: list <-> pipeline round trip with filters in the URL, return to the
// exact source view from a project, dashboard "添加成员", dialog errors shown in
// the dialog, a close button on every dialog, plus actions that had no E2E.
async function login(page: import('../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs').Page, subject: string) {
  await page.goto('/login');
  await page.locator('select').selectOption(subject);
  await action(page, 'auth.dev-login').click();
  await expect(page.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
}

test('列表↔管道双向切换保持筛选，详情返回原视图，前进后退与刷新一致', async ({ page }, info) => {
  test.skip(info.project.name !== 'desktop', 'pipeline columns are a desktop layout; mobile covered below');
  await login(page, 'synthetic-a');
  const name = uniq('R0视图');
  const created = await api(page, '/api/v1/projects', 'POST', {
    projectName: name, customer: name, location: { province: '合成省', city: '合成市', factoryName: '一厂', workshop: '一车间' },
    projectType: 'RETROFIT', salesStage: 'L2_TECHNICAL', informationSource: 'END_CUSTOMER', confidenceLevel: 'CUSTOMER_CONFIRMED',
    requirements: ['ANTI_SWAY'], requirementNotes: 'R0', plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 1 }],
  });
  expect(created.status).toBe(200);

  await page.getByRole('button', { name: '我的报备', exact: true }).click();
  await page.getByRole('textbox', { name: '搜索报备', exact: true }).fill(name);
  await page.getByLabel('报备状态筛选').selectOption('DRAFT');
  await expect(page).toHaveURL((u) => u.searchParams.get('q') === name && u.searchParams.get('status') === 'DRAFT');
  await expect(page.locator('h1 small')).toHaveText('1');

  // List -> pipeline keeps the filters.
  await action(page, 'view.pipeline').click();
  await expect(page).toHaveURL((u) => u.pathname === '/pipeline' && u.searchParams.get('q') === name && u.searchParams.get('status') === 'DRAFT');
  await expect(page.getByRole('textbox', { name: '搜索报备', exact: true })).toHaveValue(name);
  await expect(page.getByLabel('报备状态筛选')).toHaveValue('DRAFT');
  await expect(page.locator('h1 small')).toHaveText('1');
  // Open the card from its column; return lands on the same pipeline view and column.
  await action(page, 'pipeline.jump').filter({ hasText: '技术交流' }).click();
  const card = page.locator('[data-stage="L2_TECHNICAL"] .stage-card').filter({ hasText: name });
  await card.locator('[data-action-id="project.detail"]').click();
  await expect(page).toHaveURL(/\/projects\/[0-9a-f-]+\?from=/);
  await expect(action(page, 'nav.open').filter({ hasText: '返回项目管道' })).toBeVisible();
  await page.reload();
  await action(page, 'nav.open').filter({ hasText: '返回项目管道' }).click();
  await expect(page).toHaveURL((u) => u.pathname === '/pipeline' && u.searchParams.get('q') === name && u.searchParams.get('col') === 'L2_TECHNICAL');
  await expect(page.getByRole('textbox', { name: '搜索报备', exact: true })).toHaveValue(name);
  await expect(page.locator('[data-stage="L2_TECHNICAL"] .stage-card').filter({ hasText: name })).toBeVisible();

  // Pipeline -> list keeps the filters; back/forward restore each view.
  await action(page, 'view.list').click();
  await expect(page).toHaveURL((u) => u.pathname === '/projects' && u.searchParams.get('q') === name && u.searchParams.get('status') === 'DRAFT');
  await expect(page.getByLabel('报备状态筛选')).toHaveValue('DRAFT');
  await page.goBack();
  await expect(page).toHaveURL(/\/pipeline\?/);
  await expect(page.getByRole('textbox', { name: '搜索报备', exact: true })).toHaveValue(name);
  await page.goForward();
  await expect(page).toHaveURL(/\/projects\?/);
  await expect(page.getByRole('textbox', { name: '搜索报备', exact: true })).toHaveValue(name);

  // From the list, the project returns to the list with the same filters.
  await action(page, 'project.preview').first().click();
  await action(page, 'project.detail').click();
  await expect(action(page, 'nav.open').filter({ hasText: '返回我的报备' })).toBeVisible();
  await action(page, 'nav.open').filter({ hasText: '返回我的报备' }).click();
  await expect(page).toHaveURL((u) => u.pathname === '/projects' && u.searchParams.get('q') === name && u.searchParams.get('status') === 'DRAFT');
  await expect(page.locator('h1 small')).toHaveText('1');
  // A direct link with filters opens the filtered list.
  await page.goto('/projects?status=DRAFT&q=' + encodeURIComponent(name));
  await expect(page.locator('h1 small')).toHaveText('1');
  // An untrusted return target falls back to the list.
  await page.goto('/projects/' + created.body.id + '?from=' + encodeURIComponent('https://evil.test/'));
  await expect(action(page, 'nav.open').filter({ hasText: '返回我的报备' })).toBeVisible();
});

test('弹窗内显示错误与必填提示，统一关闭入口，重新加载与全局搜索', async ({ page, cleanup }) => {
  await login(page, 'synthetic-a');
  const name = uniq('R0弹窗');
  const created = await api(page, '/api/v1/projects', 'POST', {
    projectName: name, customer: name, location: { province: '合成省', city: '合成市', factoryName: '一厂', workshop: '一车间' },
    projectType: 'RETROFIT', salesStage: 'L1_REQUIREMENT', informationSource: 'END_CUSTOMER', confidenceLevel: 'CUSTOMER_CONFIRMED',
    requirements: ['ANTI_SWAY'], requirementNotes: 'R0', plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 1 }],
  });
  // Follow-ups are recorded on submitted filings; registered for cleanup first.
  cleanup.track(created.body.id);
  expect((await api(page, `/api/v1/projects/${created.body.id}/submit`, 'POST', { version: created.body.version })).status).toBe(200);
  // Global search goes to the filtered list.
  await page.getByLabel('全局搜索').fill(name);
  await page.getByLabel('全局搜索').press('Enter');
  await expect(page).toHaveURL((u) => u.pathname === '/projects' && u.searchParams.get('q') === name);
  await expect(page.locator('h1 small')).toHaveText('1');

  await page.goto('/projects/' + created.body.id);
  await action(page, 'activity.open').click();
  const dialog = page.getByRole('dialog', { name: '记录一次跟进' });
  // Empty required field: the button is usable and explains what is missing, in the dialog.
  await dialog.locator('[data-action-id="activity.create"]').click();
  await expect(dialog.getByRole('alert')).toContainText('请填写跟进摘要');
  await expect(dialog.locator('[name=summary]')).toBeFocused();
  // Server failure (network) is shown inside the dialog, input kept, retry works.
  await dialog.locator('[name=summary]').fill('R0 合成跟进');
  await page.route('**/activities', (route) => route.abort('internetdisconnected'), { times: 1 });
  await dialog.locator('[data-action-id="activity.create"]').click();
  await expect(dialog.getByRole('alert')).toContainText('网络');
  await expect(dialog.locator('[name=summary]')).toHaveValue('R0 合成跟进');
  await expect(page.locator('.error-banner')).toHaveCount(0);
  await dialog.locator('[data-action-id="activity.create"]').click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByText('R0 合成跟进', { exact: true }).first()).toBeVisible();

  // Every dialog has a close button; unsaved input asks before closing.
  await action(page, 'activity.open').click();
  await dialog.locator('[name=summary]').fill('未保存');
  await dialog.locator('[data-action-id="dialog.close"]').click();
  await action(page, 'guard.stay').click();
  await expect(dialog).toBeVisible();
  await dialog.locator('[data-action-id="dialog.close"]').click();
  await action(page, 'guard.leave').click();
  await expect(dialog).toHaveCount(0);

  // Reload after a failed detail load.
  await page.route('**/api/v1/projects/' + created.body.id, (route) => route.abort('internetdisconnected'), { times: 1 });
  await page.reload();
  await expect(action(page, 'project.reload')).toBeVisible();
  await action(page, 'project.reload').click();
  await expect(page.getByRole('heading', { name, exact: true })).toBeVisible();

  // Statistics refresh reloads from the server.
  await page.getByRole('button', { name: '统计', exact: true }).click();
  const refreshed = page.waitForResponse((r) => r.url().includes('/api/v1/stage-summary'));
  await action(page, 'statistics.refresh').first().click();
  expect((await refreshed).status()).toBe(200);
});

test('工作台“添加成员”直接打开添加表单；团队表单就近提示', async ({ page }) => {
  await login(page, 'synthetic-admin');
  await action(page, 'staff.invite-start').click();
  await expect(page).toHaveURL('/settings/users');
  const dialog = page.getByRole('dialog', { name: '添加成员' });
  await expect(dialog).toBeVisible();
  await dialog.locator('[data-action-id="dialog.close"]').click();
  await expect(dialog).toHaveCount(0);
  await page.reload();
  await expect(dialog).toHaveCount(0);

  await page.getByRole('button', { name: '团队管理', exact: true }).click();
  await action(page, 'team.create').click();
  const team = page.getByRole('dialog', { name: '新建团队' });
  await team.locator('[data-action-id="team.confirm"]').click();
  await expect(team.getByRole('alert')).toContainText('请填写团队名称');
  await expect(team.locator('[name=teamName]')).toBeFocused();
  await team.locator('[data-action-id="dialog.close"]').click();
  await expect(team).toHaveCount(0);
});

test('个人账号菜单：唯一入口，个人资料与退出登录', async ({ page }) => {
  await login(page, 'synthetic-manager');
  await expect(page.locator('[data-action-id="auth.logout"]:visible')).toHaveCount(0);
  await action(page, 'auth.account').click();
  const menu = page.getByRole('dialog', { name: '个人账号' });
  await expect(menu).toContainText('示例主管');
  await expect(menu).toContainText('销售主管');
  await expect(menu.getByText('退出企业账号')).toHaveCount(0);
  await action(page, 'auth.logout').click();
  await expect(page).toHaveURL('/login');
  expect((await page.request.get('/api/v1/me')).status()).toBe(401);
});
