import { test, expect, action, api, uniq } from './support/isolation';
import type { Page } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';

// V1.1 R7-D2 AC-13: list → detail → back restores the query, the page and the
// scroll position, through the in-app return button and through browser back;
// an old stage= bookmark keeps working.
async function login(p: Page, subject: string) {
  await p.goto('/login');
  await p.locator('select').selectOption(subject);
  await action(p, 'auth.dev-login').click();
  await expect(p.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
}
const scrollY = (p: Page) => p.evaluate(() => Math.round(window.scrollY));

test('V1.1 R7 AC-13 列表筛选、分页与滚动位置在返回后恢复', async ({ page }, info) => {
  await login(page, 'synthetic-b');
  const tag = uniq('R7滚动');
  // 30 projects in one stage: two pages of 20.
  for (let i = 0; i < 30; i += 10) {
    const batch = [];
    for (let j = i; j < i + 10; j++)
      batch.push(api(page, '/api/v1/projects', 'POST', { projectName: `${tag}-${String(j).padStart(2, '0')}`, customer: tag + '客户', salesStage: 'L2_TECHNICAL', version: 1 }));
    for (const r of await Promise.all(batch)) expect(r.status).toBe(200);
  }
  const listUrl = `/projects?q=${encodeURIComponent(tag)}&stage=L2_TECHNICAL&sort=name`;
  await page.goto(listUrl);
  await expect(page.locator('h1 small')).toHaveText('30');
  await action(page, 'project.page-next').click();
  await expect(page.locator('.pagination')).toContainText('第 2 页');
  await expect(page).toHaveURL(/page=2/);
  const rows = info.project.name === 'mobile-390' ? page.locator('.mobile-projects [data-action-id="project.preview"]') : page.locator('.table-wrap tbody tr');
  await expect(rows).toHaveCount(10);
  // Scroll so the 7th item is near the top, then open it.
  const target = rows.nth(6);
  await target.evaluate((el) => window.scrollTo(0, el.getBoundingClientRect().top + window.scrollY - 120));
  await page.waitForTimeout(150);
  const before = await scrollY(page);
  expect(before).toBeGreaterThan(100);
  const name = (await target.textContent())!.match(new RegExp(tag + '-\\d\\d'))![0];
  await target.click();
  await action(page, 'project.detail').click();
  await expect(page.getByRole('heading', { name, exact: true })).toBeVisible();
  // In-app return button.
  await action(page, 'nav.open').filter({ hasText: '返回项目中心' }).click();
  await expect(page).toHaveURL((u) => u.pathname === '/projects' && u.searchParams.get('page') === '2' && u.searchParams.get('stage') === 'L2_TECHNICAL' && u.searchParams.get('q') === tag);
  await expect(page.locator('.pagination')).toContainText('第 2 页');
  // Restored to where the user was (within one card's height), not the top.
  await expect.poll(async () => Math.abs((await scrollY(page)) - before)).toBeLessThan(60);
  // Browser back from the detail page.
  await rows.nth(6).click();
  await action(page, 'project.detail').click();
  await expect(page.getByRole('heading', { name, exact: true })).toBeVisible();
  await page.goBack();
  await expect(page).toHaveURL(/page=2/);
  await expect(page.locator('.pagination')).toContainText('第 2 页');
  // Restored to where the user was (within one card's height), not the top.
  await expect.poll(async () => Math.abs((await scrollY(page)) - before)).toBeLessThan(60);
  // Old stage= bookmark keeps the filter.
  await page.goto(`/projects?stage=L2_TECHNICAL&q=${encodeURIComponent(tag)}`);
  await expect(page.locator('h1 small')).toHaveText('30');
  await expect(page.getByLabel('阶段筛选')).toHaveValue('L2_TECHNICAL');
});
