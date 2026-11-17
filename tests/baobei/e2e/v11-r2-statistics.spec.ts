import { test, expect, action, api, uniq } from './support/isolation';
import type { Page } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';

// V1.1 R2: a supervisor switches between team, own and one member's
// statistics; the numbers match the server overview and the project list of
// the same selection; a crafted URL for another team's member is refused.
async function login(p: Page, subject: string) {
  await p.goto('/login');
  await p.locator('select').selectOption(subject);
  await action(p, 'auth.dev-login').click();
  await expect(p.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
}

test('V1.1 R2 主管：团队/本人/成员统计一致，越权成员被拒绝', async ({ page }) => {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await login(page, 'synthetic-manager');
  const tag = uniq('V11统计');
  await api(page, '/api/v1/projects', 'POST', { projectName: tag + '主管', customer: tag + '客户', salesStage: 'L2_TECHNICAL', version: 1 });
  const members = (await api(page, '/api/v1/analytics/members')).body.members as { memberId: string; name: string; total: number; others?: boolean }[];
  const salesA = members.find((m) => m.name === '示例销售 A')!;
  expect(salesA).toBeTruthy();
  const sum = members.reduce((a, m) => a + m.total, 0);
  const team = (await api(page, '/api/v1/analytics/overview?scope=team')).body;
  expect(sum).toBe(team.total);

  await page.goto('/statistics');
  await expect(page.getByTestId('stat-total')).toHaveText(String(team.total));
  await expect(page.locator('.stats-members')).toContainText('示例销售 A');
  await action(page, 'scope.select').filter({ hasText: '我的项目' }).click();
  const mine = (await api(page, '/api/v1/analytics/overview?scope=mine')).body;
  await expect(page.getByTestId('stat-total')).toHaveText(String(mine.total));
  await expect(page).toHaveURL(/scope=mine/);
  const list = (await api(page, '/api/v1/projects?scope=mine&pageSize=1')).body;
  expect(list.total).toBe(mine.total);

  await action(page, 'scope.select').filter({ hasText: '按成员' }).click();
  await page.getByLabel('选择成员').selectOption(salesA.memberId);
  const one = (await api(page, '/api/v1/analytics/overview?scope=member&memberId=' + salesA.memberId)).body;
  await expect(page.getByTestId('stat-total')).toHaveText(String(one.total));
  await expect(page.locator('.page-heading')).toContainText('成员：示例销售 A');
  expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);

  // The same selection carries over to the project list.
  await page.goto('/projects?scope=member&memberId=' + salesA.memberId);
  await expect(page.locator('h1 small')).toHaveText(String(one.total));

  // Another team's member through a crafted URL: refused, nothing shown.
  await action(page, 'auth.account').click();
  await action(page, 'auth.logout').click();
  await expect(page).toHaveURL('/login');
  await login(page, 'synthetic-manager-other');
  const refused = await api(page, '/api/v1/analytics/overview?scope=member&memberId=' + salesA.memberId);
  expect(refused.status).toBe(404);
  expect(JSON.stringify(refused.body)).not.toContain('示例销售');
  await page.goto('/statistics?scope=member&memberId=' + salesA.memberId);
  await expect(page.getByRole('alert')).toContainText('不存在或不在你的范围内');
  await expect(page.getByTestId('stat-total')).toHaveCount(0);
  expect(errors).toEqual([]);
});
