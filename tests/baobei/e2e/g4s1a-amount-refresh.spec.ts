import { type Browser, type Page, type Route } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
import { test, expect, action } from './support/isolation';
// G4-S1A: the amount panel never presents an old figure as the current
// result. Success → amount and its time; a failed refresh → explicit failure,
// old figure withheld; retry success → new amount and new time.
// Responses are the real server's, with deterministic markers substituted.
async function session(browser: Browser, subject: string) {
  const p = await (await browser.newContext({ baseURL: 'http://127.0.0.1:5188', viewport: { width: 1440, height: 900 } })).newPage();
  await p.goto('/login');
  await p.locator('select').selectOption(subject);
  await action(p, 'auth.dev-login').click();
  await expect(p.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
  return p;
}
const when = (p: Page, iso: string) =>
  p.evaluate((iso) => new Date(iso).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }), iso);

test('G4-S1A 金额面板：刷新失败不展示旧金额，重试成功显示新金额与新统计时间', async ({ browser }, info) => {
  test.skip(info.project.name !== 'desktop', 'once');
  const p = await session(browser, 'synthetic-director');
  // Each stage-summary request k: k=1 ok (old), k=2 fails, k=3 ok (new).
  const plan: { ok: boolean; total?: string; at?: string }[] = [
    { ok: true, total: '111111.11', at: '2026-03-04T05:01:00Z' },
    { ok: false },
    { ok: true, total: '222222.22', at: '2026-03-04T05:03:00Z' },
  ];
  let n = 0;
  await p.route('**/api/v1/stage-summary', async (route: Route) => {
    const step = plan[Math.min(n++, plan.length - 1)];
    if (!step.ok)
      return route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ code: 'SERVICE_UNAVAILABLE', message: '服务暂时不可用，请保留输入并重试' }) });
    const res = await route.fetch();
    const json = await res.json();
    json.amount.knownTotal = step.total;
    json.asOf = step.at;
    await route.fulfill({ response: res, json });
  });
  const total = p.getByTestId('home-known-total');
  const stageAsOf = p.getByTestId('home-stage-asof');

  // 1. First load succeeds: amount and its statistics time.
  await p.goto('/');
  await expect(total).toHaveText('¥111,111.11');
  await expect(stageAsOf).toHaveText('阶段与金额统计时间 ' + (await when(p, plan[0].at!)));

  // 2. Refresh fails: explicit failure; the previous amount and its time are not shown.
  await action(p, 'dashboard.refresh').click();
  const failure = p.getByTestId('home-amount-error');
  await expect(failure).toContainText('本次刷新失败');
  await expect(failure).toContainText('上次成功统计（' + (await when(p, plan[0].at!)) + '）的金额已隐藏');
  await expect(total).toHaveCount(0);
  await expect(p.locator('.home-amount')).not.toContainText('111,111.11');
  await expect(stageAsOf).toHaveCount(0);
  await expect(p.locator('.stage-bars')).toHaveCount(0);

  // 3. Retry succeeds: new amount with the new time.
  await action(p, 'dashboard.stage-reload').click();
  await expect(total).toHaveText('¥222,222.22');
  await expect(stageAsOf).toHaveText('阶段与金额统计时间 ' + (await when(p, plan[2].at!)));
  await expect(failure).toHaveCount(0);
  expect(n).toBe(3);
  await p.context().close();
});
