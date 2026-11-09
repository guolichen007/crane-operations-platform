import { type Browser, type Page, type Route } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
import { test, expect, action, api } from './support/isolation';
// G3-4-A1 data consistency of the home page. workspace-summary and
// stage-summary are two independent snapshots: each panel states its own
// statistics time, and when refreshes overlap an older response that arrives
// late never replaces a newer one — for either panel.
async function session(browser: Browser, subject: string) {
  const ctx = await browser.newContext({ baseURL: 'http://127.0.0.1:5188', viewport: { width: 1440, height: 900 } });
  const p = await ctx.newPage();
  await p.goto('/login');
  await p.locator('select').selectOption(subject);
  await action(p, 'auth.dev-login').click();
  await expect(p.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
  return p;
}
const when = (p: Page, iso: string) =>
  p.evaluate((iso) => new Date(iso).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }), iso);

test('G3-4-A1 两个快照各自显示统计时间', async ({ browser }, info) => {
  test.skip(info.project.name !== 'desktop', 'once');
  const p = await session(browser, 'synthetic-director');
  // Distinct, controlled times so the two labels cannot coincide.
  const wsAt = '2026-03-04T05:06:00Z', ssAt = '2026-03-04T05:09:00Z';
  for (const [path, at] of [['workspace-summary', wsAt], ['stage-summary', ssAt]] as const)
    await p.route(`**/api/v1/${path}`, async (route: Route) => {
      const res = await route.fetch();
      await route.fulfill({ response: res, json: { ...(await res.json()), asOf: at } });
    });
  await p.goto('/');
  await expect(p.locator('.stage-bars')).toBeVisible();
  await expect(p.locator('.scope-asof')).toContainText('统计时间 ' + (await when(p, wsAt)));
  await expect(p.getByTestId('home-stage-asof')).toHaveText('阶段与金额统计时间 ' + (await when(p, ssAt)));
  // Failing stage panel: no stale time is shown next to the error.
  await p.unrouteAll();
  await p.route('**/api/v1/stage-summary', (r) => r.abort('failed'));
  await p.goto('/');
  await expect(p.locator('.home-stages').getByRole('alert')).toBeVisible();
  await expect(p.getByTestId('home-stage-asof')).toHaveCount(0);
  await p.context().close();
});

test('G3-4-A1 快速连续刷新：阶段面板的旧响应晚到不覆盖新结果', async ({ browser }, info) => {
  test.skip(info.project.name !== 'desktop', 'once');
  const p = await session(browser, 'synthetic-director');
  await expect(p.locator('.stage-bars')).toBeVisible();
  const real = (await api(p, '/api/v1/stage-summary')).body;
  let n = 0;
  const released: number[] = [];
  await p.route('**/api/v1/stage-summary', async (route: Route) => {
    const mine = ++n;
    const res = await route.fetch();
    const json = await res.json();
    // Request k reports stage[0].count = 1000+k and asOf minute k.
    json.stages[0].count = 1000 + mine;
    json.asOf = `2026-03-04T05:${String(10 + mine).padStart(2, '0')}:00Z`;
    if (mine === 1) await new Promise((r) => setTimeout(r, 1500)); // older refresh answers last
    released.push(mine);
    await route.fulfill({ response: res, json });
  });
  const refresh = action(p, 'dashboard.refresh');
  await refresh.click();
  await refresh.click();
  const bar = action(p, 'dashboard.stage').first().locator('.stage-bar-count');
  await expect(bar).toHaveText('1002');
  await expect.poll(() => released.length, { timeout: 5000 }).toBe(2);
  expect(released).toEqual([2, 1]); // the stale response really did arrive after the newer one
  await p.evaluate(() => new Promise(requestAnimationFrame));
  await expect(bar).toHaveText('1002');
  await expect(p.getByTestId('home-stage-asof')).toHaveText('阶段与金额统计时间 ' + (await when(p, '2026-03-04T05:12:00Z')));
  expect(real.stages).toHaveLength(12);
  await p.context().close();
});
