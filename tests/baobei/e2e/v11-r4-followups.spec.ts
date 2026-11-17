import { test, expect, action, api, uniq } from './support/isolation';
import type { Page } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';

// V1.1 R4 (AC-18): follow-up center windows, counts equal to the server and
// to the home reminder, record a follow-up from the center, old links.
async function login(p: Page, subject: string) {
  await p.goto('/login');
  await p.locator('select').selectOption(subject);
  await action(p, 'auth.dev-login').click();
  await expect(p.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
}

test('V1.1 R4 跟进中心：四个时间窗口、登记跟进后移动、首页提醒一致', async ({ page, cleanup }) => {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await login(page, 'synthetic-b');
  const tag = uniq('V11跟进');
  const hour = 3600_000;
  const mk = async (suffix: string, at: string | null) => {
    const r = await api(page, '/api/v1/projects', 'POST', {
      projectName: tag + suffix, customer: tag + '客户' + suffix, salesStage: 'L1_REQUIREMENT', nextAction: '下一步' + suffix, nextFollowupAt: at, version: 1,
      location: { province: '合成省', city: '合成市', factoryName: tag + suffix + '厂', workshop: '一车间' }, projectType: 'RETROFIT',
      informationSource: 'END_CUSTOMER', confidenceLevel: 'CUSTOMER_CONFIRMED', requirements: ['ANTI_SWAY'], requirementNotes: '合成需求',
      plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 1 }],
    });
    expect(r.status).toBe(200);
    return r.body;
  };
  await mk('-逾期', new Date(Date.now() - 2 * hour).toISOString());
  // Later today in Beijing time if possible, otherwise this case is skipped below.
  const bj = new Date(Date.now() + 8 * hour);
  const minutesLeft = 24 * 60 - (bj.getUTCHours() * 60 + bj.getUTCMinutes());
  const today = minutesLeft > 10 ? await mk('-今天', new Date(Date.now() + 5 * 60_000).toISOString()) : null;
  await mk('-三天后', new Date(Date.now() + 3 * 24 * hour).toISOString());
  await mk('-未安排', null);

  await page.goto('/followups');
  const q = encodeURIComponent(tag);
  const server = (await api(page, '/api/v1/followups?window=today&q=' + q)).body;
  expect(server.counts).toMatchObject({ overdue: 1, week: 1, unscheduled: 1, today: today ? 1 : 0 });
  await page.getByRole('textbox', { name: '搜索报备' }).fill(tag);
  for (const [label, n] of [['已逾期', 1], ['未来 7 天', 1], ['未安排', 1]] as const) {
    await action(page, 'followup.window').filter({ hasText: label }).click();
    await expect(page.locator('h1 small')).toHaveText(String(n));
  }
  await expect(page.locator('.followup-list')).toContainText('-未安排');
  // Old link keeps working: window=upcoming → 未来 7 天.
  await page.goto('/followups?window=upcoming&q=' + q);
  await expect(action(page, 'followup.window').filter({ hasText: '未来 7 天' })).toHaveAttribute('aria-selected', 'true');
  // Record a follow-up from the center (submitted project, owner).
  const sub = (await api(page, '/api/v1/projects?q=' + encodeURIComponent(tag + '-逾期'))).body.items[0];
  cleanup.track(sub.id);
  const s = await api(page, `/api/v1/projects/${sub.id}/submit`, 'POST', { version: sub.version });
  expect(s.status).toBe(200);
  await page.goto('/followups?window=overdue&q=' + q);
  await action(page, 'followup.record').first().click();
  await expect(page).toHaveURL(/followup=1/);
  const dialog = page.getByRole('dialog');
  await dialog.locator('textarea').first().fill('电话确认需求');
  await dialog.locator('[name=nextAction]').fill('安排技术交流');
  const next = new Date(Date.now() + 4 * 24 * hour + 8 * hour);
  await dialog.locator('input[type=datetime-local]').last().fill(next.toISOString().slice(0, 16));
  await action(page, 'activity.create').click();
  await expect(dialog).toHaveCount(0);
  await action(page, 'nav.open').filter({ hasText: '返回跟进中心' }).click();
  await expect(page.locator('h1 small')).toHaveText('0');
  const after = (await api(page, '/api/v1/followups?window=week&q=' + q)).body;
  expect(after.total).toBe(2);
  // Home reminder tiles show the same numbers as the center (whole scope).
  const center = (await api(page, '/api/v1/followups?window=overdue')).body.counts;
  const home = (await api(page, '/api/v1/workspace-summary')).body.counts;
  expect(home.followupOverdue).toBe(center.overdue);
  expect(home.followupToday).toBe(center.today);
  // In-app reminder badge on the navigation: overdue + today, from the server.
  await page.goto('/');
  const badge = page.getByTestId('followup-reminder');
  if (center.overdue + center.today > 0) {
    await expect(badge).toHaveText(String(center.overdue + center.today));
    // The badge never changes the entry's accessible name (CI 44038625800:
    // exact-name navigation broke once the shared database had reminders).
    if ((page.viewportSize()?.width ?? 1440) > 700)
      await expect(page.getByRole('button', { name: '跟进中心', exact: true })).toBeVisible();
  }
  else await expect(badge).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect(errors).toEqual([]);
});
