import {
  test,
  expect,
  type Page,
  type Route,
} from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
// G3-3-P0 R1: deterministic reproduction of the review-queue view-switch race
// seen in CI Run 42365532593 (g32b-queue.spec.ts, mobile-390, "下一页" never
// enabled). The queue API is replaced by a controlled fake with per-request
// delays so the timing does not depend on the shared database contents.
const action = (p: Page, id: string) => p.locator(`[data-action-id="${id}"]:visible`);
const loading = (p: Page) => p.getByRole('status').filter({ hasText: '正在加载审核队列' });
type View = 'reviewable' | 'blocked' | 'all';
const totals: Record<View, number> = { reviewable: 3, blocked: 30, all: 45 };
function item(view: View, i: number) {
  const reviewable = view === 'reviewable' || (view === 'all' && i < 3);
  return {
    id: `00000000-0000-4000-8000-${String(i).padStart(12, '0')}`,
    registrationNo: `BB-R1-${view}-${String(i).padStart(3, '0')}`,
    projectName: `R1队列${view}${i}`,
    customer: 'R1客户',
    intakeStatus: 'SUBMITTED',
    salesStage: 'L1_REQUIREMENT',
    location: { province: '合成省', city: '合成市' },
    submittedAt: new Date(Date.now() - 3_600_000 * (i + 2)).toISOString(),
    review: { eligible: reviewable, reasonCode: reviewable ? null : 'REVIEW_CONFLICT', nextAction: 'approve' },
    actions: {},
  };
}
// delays[view][page] in ms; requests records every queue request in order.
async function fakeQueue(p: Page, delays: Partial<Record<View, Record<number, number>>>) {
  const requests: string[] = [];
  await p.route('**/api/v1/reviews?*', async (route: Route) => {
    const url = new URL(route.request().url());
    const view = (url.searchParams.get('eligibility') || 'reviewable') as View;
    const page = Number(url.searchParams.get('page') || '1');
    const size = Number(url.searchParams.get('pageSize') || '20');
    requests.push(`${view}:${page}`);
    const wait = delays[view]?.[page] ?? 30;
    await new Promise((r) => setTimeout(r, wait));
    const all = Array.from({ length: totals[view] }, (_, i) => item(view, i));
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ items: all.slice((page - 1) * size, page * size), total: totals[view], counts: totals, asOf: new Date().toISOString() }),
    });
  });
  return requests;
}
async function login(p: Page) {
  await p.goto('/login');
  await p.locator('select').selectOption('synthetic-manager');
  await action(p, 'auth.dev-login').click();
  await expect(p.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
}
const tab = (p: Page, label: string) => action(p, 'review.view').filter({ hasText: label });

test('R1 视图切换加载期间分页不可用（旧 total 不能驱动分页）', async ({ page }) => {
  await login(page);
  await fakeQueue(page, { reviewable: { 1: 1500 } });
  await page.goto('/reviews?eligibility=all');
  await expect(loading(page)).toHaveCount(0);
  await expect(page.locator('.pagination')).toContainText('45 项 · 第 1 页');
  await expect(action(page, 'project.page-next')).toBeEnabled();
  await tab(page, '我可审核').click();
  await expect(loading(page)).toHaveCount(1);
  // The previous view's total (45) must not keep "下一页" enabled while the
  // reviewable view (3 items) is still loading. Read in one frame: an
  // auto-retrying toBeDisabled() would wait for the load and hide the race.
  const during = await page.evaluate(() => ({
    loading: !!document.querySelector('[role=status][aria-busy=true]'),
    next: (document.querySelector('[data-action-id="project.page-next"]') as HTMLButtonElement).disabled,
    prev: (document.querySelector('[data-action-id="project.page-prev"]') as HTMLButtonElement).disabled,
  }));
  expect(during).toEqual({ loading: true, next: true, prev: true });
  await expect(loading(page)).toHaveCount(0);
  await expect(page.locator('.pagination')).toContainText('3 项 · 第 1 页');
  await expect(action(page, 'project.page-next')).toBeDisabled();
  await expect(action(page, 'review.open')).toHaveCount(3);
});

test('R1 翻页加载期间不可重复翻页，末页正确停止', async ({ page }) => {
  await login(page);
  const requests = await fakeQueue(page, { all: { 2: 1200 } });
  await page.goto('/reviews?eligibility=all');
  await expect(loading(page)).toHaveCount(0);
  await action(page, 'project.page-next').click();
  await expect(loading(page)).toHaveCount(1);
  expect(await page.evaluate(() => (document.querySelector('[data-action-id="project.page-next"]') as HTMLButtonElement).disabled)).toBe(true);
  await expect(loading(page)).toHaveCount(0);
  await expect(page.locator('.pagination')).toContainText('第 2 页');
  await action(page, 'project.page-next').click();
  await expect(page.locator('.pagination')).toContainText('第 3 页');
  await expect(loading(page)).toHaveCount(0);
  await expect(action(page, 'review.open')).toHaveCount(5);
  await expect(action(page, 'project.page-next')).toBeDisabled();
  expect(requests.filter((r) => r.startsWith('all:'))).toEqual(['all:1', 'all:2', 'all:3']);
});

test('R1 旧请求晚于新请求返回时不覆盖当前视图', async ({ page }) => {
  await login(page);
  const requests = await fakeQueue(page, { blocked: { 1: 1500 }, reviewable: { 1: 200 } });
  await page.goto('/reviews?eligibility=all');
  await expect(loading(page)).toHaveCount(0);
  const slowBlocked = page.waitForResponse((r) => r.url().includes('eligibility=blocked'));
  await tab(page, '需要其他人处理').click();
  await tab(page, '我可审核').click();
  await expect(loading(page)).toHaveCount(0);
  await expect(tab(page, '我可审核')).toHaveAttribute('aria-selected', 'true');
  await expect(action(page, 'review.open')).toHaveCount(3);
  // The slow blocked response arrives after the current view rendered; it must be discarded.
  await slowBlocked;
  await page.evaluate(() => new Promise(requestAnimationFrame));
  await expect(action(page, 'review.open')).toHaveCount(3);
  await expect(action(page, 'review.open').first()).toContainText('R1队列reviewable0');
  await expect(page.locator('.pagination')).toContainText('3 项 · 第 1 页');
  expect(requests).toEqual(['all:1', 'blocked:1', 'reviewable:1']);
});

// The pre-R1 helper from g32b-queue.spec.ts, verbatim.
async function oldFindCard(p: Page, text: string) {
  const card = action(p, 'review.open').filter({ hasText: text });
  for (let i = 0; i < 20 && !(await card.count()); i++) {
    const next = action(p, 'project.page-next');
    if (await next.isDisabled()) break;
    await next.click();
    await expect(p.getByRole('status').filter({ hasText: '正在加载审核队列' })).toHaveCount(0);
  }
  return card;
}

test('R1 CI 失败路径：“全部待审→我可审核”加载中调用原 findCard 不再卡死', async ({ page }) => {
  test.setTimeout(20000);
  await login(page);
  await fakeQueue(page, { reviewable: { 1: 1500 } });
  await page.goto('/reviews?eligibility=all');
  await expect(loading(page)).toHaveCount(0);
  await tab(page, '我可审核').click(); // same sequence as g32b-queue.spec.ts:124-125
  const card = await oldFindCard(page, 'BB-R1-reviewable-001');
  await expect(card).toHaveCount(1, { timeout: 5000 });
  await expect(page.locator('.pagination')).toContainText('3 项 · 第 1 页');
});

test('R1 CI 原始症状：isDisabled() 读到旧状态后加载完成，click 等待已禁用按钮', async ({ page }) => {
  await login(page);
  await fakeQueue(page, { reviewable: { 1: 800 } });
  await page.goto('/reviews?eligibility=all');
  await expect(loading(page)).toHaveCount(0);
  await tab(page, '我可审核').click();
  const next = action(page, 'project.page-next');
  // Step 1 of the old helper, executed while the new view is loading.
  const sawDisabled = await next.isDisabled();
  // The response lands between the helper's check and its click.
  await expect(loading(page)).toHaveCount(0);
  // Step 2 of the old helper. Pre-R1: the check said "enabled", the button is
  // now disabled (3 items), and click() waits for "enabled" until timeout —
  // the "element is not enabled" loop of CI Run 42365532593.
  let clickError = '';
  if (!sawDisabled) await next.click({ timeout: 1500 }).catch((e: Error) => (clickError = e.message));
  expect({ sawDisabled, clickError: clickError.includes('not enabled') ? 'not enabled' : clickError }).toEqual({ sawDisabled: true, clickError: '' });
});

test('R1 目标项目在后页：切换视图加载中开始逐页查找（真实 UI，45 项、延迟翻页）', async ({ page }) => {
  await login(page);
  await fakeQueue(page, { reviewable: { 1: 600 }, all: { 1: 400, 2: 700, 3: 300 } });
  await page.goto('/reviews?eligibility=reviewable');
  await expect(loading(page)).toHaveCount(0);
  await tab(page, '全部待审').click();
  // The pre-R1 helper gives up at once when called mid-load (paging is now
  // disabled while loading); the R1 helper waits for the view, then pages.
  expect(await (await oldFindCard(page, 'BB-R1-all-044')).count()).toBe(0);
  const card = await newFindCard(page, 'BB-R1-all-044');
  await expect(card).toHaveCount(1);
  await expect(page.locator('.pagination')).toContainText('45 项 · 第 3 页');
});

// Copy of the R1 findCard in g32b-queue.spec.ts (spec files cannot import each other).
async function newFindCard(p: Page, text: string) {
  await expect(loading(p)).toHaveCount(0);
  const card = action(p, 'review.open').filter({ hasText: text });
  const pager = p.locator('.pagination > span');
  for (let i = 0; i < 60 && !(await card.count()); i++) {
    const before = (await pager.textContent()) || '';
    const next = action(p, 'project.page-next');
    if (await next.isDisabled()) break;
    const loaded = p.waitForResponse((r) => r.url().includes('/api/v1/reviews?'));
    await next.click();
    await loaded;
    await expect(loading(p)).toHaveCount(0);
    await expect(pager).not.toHaveText(before);
  }
  if (!(await card.count())) throw new Error(`review card not found: target=${text} view=${new URL(p.url()).search} pager=${await pager.textContent()}`);
  return card;
}
