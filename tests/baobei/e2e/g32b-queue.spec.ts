import { fileURLToPath } from 'node:url';
import {
  test,
  expect,
  type Browser,
  type Page,
} from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
// G3-2B: the home tile and the review queue describe the same set; the queue
// has three server-filtered views; a stale list never authorizes a decision.
const action = (p: Page, id: string) => p.locator(`[data-action-id="${id}"]:visible`);
const shot = (name: string) => fileURLToPath(new URL(`../../../local-private/baobei/g32b-evidence/${name}.png`, import.meta.url));
async function session(browser: Browser, subject: string, viewport: { width: number; height: number } | null) {
  const ctx = await browser.newContext({ baseURL: 'http://127.0.0.1:5188', viewport: viewport ?? undefined });
  const p = await ctx.newPage();
  await p.goto('/login');
  await p.locator('select').selectOption(subject);
  await action(p, 'auth.dev-login').click();
  await expect(p.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
  return p;
}
async function api(p: Page, path: string, method = 'GET', body?: unknown) {
  return p.evaluate(
    async ({ path, method, body }) => {
      const me = await (await fetch('/api/v1/me')).json();
      const r = await fetch(path, {
        method,
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': me.csrfToken, 'Idempotency-Key': 'g32b-' + crypto.randomUUID() },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      return { status: r.status, body: await r.json() };
    },
    { path, method, body }
  );
}
// The queue is ordered oldest submission first; in a shared database new items
// can be on later pages. G3-3P0-R1: callers first wait for the requested view
// (showView), then page forward one confirmed page at a time; when the card is
// not found the failure states view, page, total and target instead of waiting
// on a disabled button until the test timeout.
const loadingQueue = (p: Page) => p.getByRole('status').filter({ hasText: '正在加载审核队列' });
async function showView(p: Page, label: string, eligibility: string) {
  const views = action(p, 'review.view');
  const loaded = p.waitForResponse((r) => r.url().includes('/api/v1/reviews?') && r.url().includes(`eligibility=${eligibility}&page=1&`));
  await views.filter({ hasText: label }).click();
  await loaded;
  await expect(p).toHaveURL(new RegExp(`eligibility=${eligibility}`));
  await expect(views.filter({ hasText: label })).toHaveAttribute('aria-selected', 'true');
  await expect(loadingQueue(p)).toHaveCount(0);
}
async function findCard(p: Page, text: string) {
  await expect(loadingQueue(p)).toHaveCount(0);
  const card = action(p, 'review.open').filter({ hasText: text });
  const pager = p.locator('.pagination > span');
  for (let i = 0; i < 60 && !(await card.count()); i++) {
    const before = (await pager.textContent()) || '';
    const next = action(p, 'project.page-next');
    if (await next.isDisabled()) break;
    const loaded = p.waitForResponse((r) => r.url().includes('/api/v1/reviews?'));
    await next.click();
    await loaded;
    await expect(loadingQueue(p)).toHaveCount(0);
    await expect(pager).not.toHaveText(before);
  }
  if (!(await card.count()))
    throw new Error(`review card not found: target=${text} view=${new URL(p.url()).search} pager=${await pager.textContent()}`);
  return card;
}
async function submit(p: Page, name: string) {
  const r = await api(p, '/api/v1/projects', 'POST', {
    projectName: name,
    customer: name + '客户',
    location: { province: '合成省', city: '合成市', factoryName: '合成一厂', workshop: name },
    projectType: 'RETROFIT',
    salesStage: 'L1_REQUIREMENT',
    informationSource: 'END_CUSTOMER',
    confidenceLevel: 'CUSTOMER_CONFIRMED',
    requirements: ['ANTI_SWAY'],
    requirementNotes: '合成需求',
    plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 2 }],
  });
  expect(r.status).toBe(200);
  const s = await api(p, `/api/v1/projects/${r.body.id}/submit`, 'POST', { version: r.body.version });
  expect(s.status).toBe(200);
  return s.body;
}

test('G3-2B 首页可审数与审核队列一致，三视图与过期按钮', async ({ browser }, info) => {
  const viewport = info.project.use.viewport ?? null;
  const tag = 'G32B' + info.project.name + Date.now();
  const sales = await session(browser, 'synthetic-a', viewport);
  const mine = await submit(sales, tag + '可审');
  // A project the manager hands off becomes one the manager cannot review.
  const draft = await api(sales, '/api/v1/projects', 'POST', { projectName: tag + '经手', customer: tag + '经手客户', version: 1 });
  expect(draft.status).toBe(200);
  const manager = await session(browser, 'synthetic-manager', viewport);
  const moved = await api(manager, `/api/v1/projects/${draft.body.id}/ownership-transfer`, 'POST', { version: draft.body.version, reason: '合成交接', targetSubject: 'synthetic-b' });
  expect(moved.status).toBe(200);
  const other = await session(browser, 'synthetic-b', viewport);
  const filled = await api(other, `/api/v1/projects/${draft.body.id}`, 'PATCH', {
    version: moved.body.version, projectName: tag + '经手', customer: tag + '经手客户',
    location: { province: '合成省', city: '合成市', factoryName: '合成一厂', workshop: tag + '经手' },
    projectType: 'RETROFIT', salesStage: 'L1_REQUIREMENT', informationSource: 'END_CUSTOMER', confidenceLevel: 'CUSTOMER_CONFIRMED',
    requirements: ['ANTI_SWAY'], requirementNotes: '合成需求', plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 2 }],
  });
  expect(filled.status).toBe(200);
  const handled = await api(other, `/api/v1/projects/${draft.body.id}/submit`, 'POST', { version: filled.body.version });
  expect(handled.status).toBe(200);

  // Home tile → reviewable view; same number in the same data state.
  await manager.goto('/');
  const summary = (await api(manager, '/api/v1/workspace-summary')).body.counts;
  const tile = action(manager, 'dashboard.task').filter({ hasText: '你可独立审核' });
  await expect(tile).toContainText(String(summary.reviewable));
  await tile.click();
  await expect(manager).toHaveURL(/\/reviews\?eligibility=reviewable$/);
  const views = action(manager, 'review.view');
  await expect(views.filter({ hasText: '我可审核' })).toHaveAttribute('aria-selected', 'true');
  await expect(views.filter({ hasText: '我可审核' })).toContainText(String(summary.reviewable));
  await expect(views.filter({ hasText: '全部待审' })).toContainText(String(summary.reviewQueue));
  const q = (await api(manager, '/api/v1/reviews?kind=pending&eligibility=reviewable&pageSize=100')).body;
  expect(q.total).toBe(summary.reviewable);
  const card = await findCard(manager, mine.registrationNo);
  await expect(card).toContainText('你可以审核');
  await expect(card).toContainText('下一步：审核');
  await expect(card).toContainText(/等待/);
  expect(q.items.some((x: { id: string }) => x.id === draft.body.id)).toBe(false);
  expect(await manager.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);
  await manager.screenshot({ path: shot(`${info.project.name}-reviewable`), fullPage: true });

  // Blocked view explains why with a fixed reason.
  await showView(manager, '需要其他人处理', 'blocked');
  const blocked = await findCard(manager, tag + '经手');
  await expect(blocked).toContainText('需要其他人处理：你参与过此项目，需由其他独立审核人处理');
  const blockedApi = (await api(manager, '/api/v1/reviews?kind=pending&eligibility=blocked&pageSize=100')).body;
  expect(blockedApi.items.some((x: { id: string }) => x.id === mine.id)).toBe(false);
  expect(blockedApi.items.some((x: { id: string }) => x.id === draft.body.id)).toBe(true);
  await manager.screenshot({ path: shot(`${info.project.name}-blocked`), fullPage: true });
  await showView(manager, '全部待审', 'all');
  const all = (await api(manager, '/api/v1/reviews?kind=pending&eligibility=all&pageSize=100')).body;
  expect(all.items.filter((x: { projectName: string }) => x.projectName.startsWith(tag)).length).toBe(2);
  await expect(views.filter({ hasText: '全部待审' })).toContainText(String(all.counts.all));

  // Stale list: another reviewer returns the project after the manager opened it.
  await showView(manager, '我可审核', 'reviewable');
  await (await findCard(manager, mine.registrationNo)).click();
  await expect(manager.getByRole('dialog')).toContainText(mine.projectName);
  const director = await session(browser, 'synthetic-director', viewport);
  const current = (await api(director, `/api/v1/projects/${mine.id}`)).body;
  expect((await api(director, `/api/v1/projects/${mine.id}/return`, 'POST', { version: current.version, reason: '合成并发退回' })).status).toBe(200);
  await action(manager, 'review.approve').click();
  await manager.locator('[name=reviewReason]').fill('合成过期按钮');
  await action(manager, 'review.confirm').click();
  await expect(manager.getByRole('dialog').getByRole('alert')).toContainText('项目已被其他人更新');
  await expect(manager.locator('[name=reviewReason]')).toHaveValue('合成过期按钮');
  expect((await api(manager, `/api/v1/projects/${mine.id}`)).body.intakeStatus).toBe('RETURNED');
  await action(manager, 'dialog.cancel').click();
  await action(manager, 'guard.leave').click();
  await action(manager, 'project.preview-close').click();
  const after = (await api(manager, '/api/v1/reviews?kind=pending&eligibility=all&pageSize=100')).body;
  expect(after.items.some((x: { id: string }) => x.id === mine.id)).toBe(false);
  for (const p of [sales, manager, other, director]) await p.context().close();
});
