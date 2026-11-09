import { fileURLToPath } from 'node:url';
import { type Browser, type Page, type Route } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
import { test, expect, uniq } from './support/isolation';
// G3-3B: statistics page with real API amounts, plus the G3-3A pipeline
// leftovers (no endless "load more" after data moved; target stage
// re-validated after 409). The shared e2e database accumulates projects from
// other specs, so amounts are asserted under a unique search tag.
const action = (p: Page, id: string) => p.locator(`[data-action-id="${id}"]:visible`);
const shot = (name: string) => fileURLToPath(new URL(`../../../local-private/baobei/g33b-evidence/${name}.png`, import.meta.url));
async function session(browser: Browser, subject: string, viewport: { width: number; height: number }) {
  const ctx = await browser.newContext({ baseURL: 'http://127.0.0.1:5188', viewport, ...(viewport.width <= 390 ? { isMobile: true, hasTouch: true } : {}) });
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
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': me.csrfToken, 'Idempotency-Key': 'g33b-' + crypto.randomUUID() },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      return { status: r.status, body: await r.json() };
    },
    { path, method, body }
  );
}
async function create(p: Page, name: string, stage: string, amount: number | null, known = true) {
  const r = await api(p, '/api/v1/projects', 'POST', {
    projectName: name,
    customer: name + '客户',
    location: { province: '合成省', city: '合成市', factoryName: name, workshop: '一车间' },
    projectType: 'RETROFIT',
    salesStage: stage,
    informationSource: 'END_CUSTOMER',
    confidenceLevel: 'CUSTOMER_CONFIRMED',
    requirements: ['ANTI_SWAY'],
    requirementNotes: '合成需求',
    plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 2 }, { equipmentType: 'GANTRY', quantity: 1 }],
    estimatedAmount: amount,
    ...(amount !== null && known ? { knowledge: { estimatedAmount: 'KNOWN' } } : {}),
  });
  expect(r.status).toBe(200);
  return r.body;
}
const noOverflow = (p: Page) => p.evaluate(() => document.documentElement.scrollWidth <= innerWidth);

for (const vp of [
  { name: '1440', width: 1440, height: 900 },
  { name: '1366', width: 1366, height: 768 },
  { name: '768', width: 768, height: 1024 },
  { name: '390', width: 390, height: 844 },
]) {
  test(`G3-3B 统计页真实金额与阶段对照 @${vp.name}`, async ({ browser }, info) => {
    test.skip(info.project.name !== 'desktop', 'viewports are set explicitly; run once');
    const tag = uniq(`G33B${vp.name}`);
    const sales = await session(browser, 'synthetic-a', vp);
    await create(sales, `${tag}报价甲`, 'L5_QUOTATION', 125000.25);
    await create(sales, `${tag}报价乙`, 'L5_QUOTATION', 0.1);
    await create(sales, `${tag}报价零`, 'L5_QUOTATION', 0); // explicit KNOWN 0
    await create(sales, `${tag}报价未知`, 'L5_QUOTATION', null);
    await create(sales, `${tag}方案旧零`, 'L4_SOLUTION', 0, false); // unflagged 0 → LEGACY_UNCONFIRMED
    await create(sales, `${tag}成交`, 'L1_REQUIREMENT', 99999999999.99);
    await sales.goto('/statistics');
    await expect(sales.getByRole('heading', { name: '统计', exact: true })).toBeVisible();
    await sales.getByRole('textbox', { name: '搜索报备' }).fill(tag);
    const api1 = (await api(sales, '/api/v1/stage-summary?q=' + encodeURIComponent(tag))).body;
    expect(api1.amount).toEqual({ knownTotal: '100000125000.34', knownCount: 4, knownZeroCount: 1, knownImplicitCount: 0, unknownCount: 1, legacyUnconfirmedCount: 1, invalidCount: 0 });
    await expect(sales.getByTestId('stat-total')).toHaveText('6');
    await expect(sales.getByTestId('stat-known-total')).toHaveText('¥100,000,125,000.34');
    await expect(sales.getByTestId('stat-known-count')).toContainText('来自 4 个项目（其中 1 个确认为 0）');
    await expect(sales.getByTestId('stat-unknown')).toHaveText('1');
    await expect(sales.getByTestId('stat-legacy')).toHaveText('1');
    await expect(sales.getByTestId('stat-invalid')).toHaveText('0');
    await expect(sales.locator('.stats-scope')).toContainText('项目预估金额');
    await expect(sales.locator('.page-heading')).toContainText('报备状态：全部（含草稿、退回）');
    const mobile = vp.width <= 700;
    if (mobile) {
      const card = sales.locator('.stats-cards [data-action-id="statistics.open-stage"]').filter({ hasText: '报价' });
      await expect(card).toContainText('4 个项目');
      await expect(card).toContainText('¥125,000.35');
      await expect(card).toContainText('已知金额 3 个 · 未计入 1 个');
    } else {
      const row = sales.locator('tr[data-stage="L5_QUOTATION"]');
      await expect(row.locator('td').nth(0)).toHaveText('4');
      await expect(row.locator('td').nth(1)).toHaveText('¥125,000.35');
      await expect(row.locator('td').nth(2)).toHaveText('3');
      await expect(row.locator('td').nth(3)).toHaveText('1 / 0 / 0');
      await expect(sales.locator('tr[data-stage="L4_SOLUTION"] td').nth(3)).toHaveText('0 / 1 / 0');
      await expect(sales.locator('.stats-table tfoot td').nth(1)).toHaveText('¥100,000,125,000.34');
    }
    expect(await noOverflow(sales)).toBe(true);
    await sales.evaluate(() => scrollTo(0, 0));
    await sales.screenshot({ path: shot(`statistics-sales-${vp.name}`), fullPage: true });
    // Status filter changes the scope of the numbers visibly and consistently.
    await sales.getByRole('combobox', { name: '报备状态筛选' }).selectOption('SUBMITTED');
    await expect(sales.locator('.page-heading')).toContainText('报备状态：已提交');
    await expect(sales.getByTestId('stat-total')).toHaveText('0');
    await sales.getByRole('combobox', { name: '报备状态筛选' }).selectOption('');
    await expect(sales.getByTestId('stat-total')).toHaveText('6');
    // Click-through opens the same-scope project list for the stage.
    if (mobile) await sales.locator('.stats-cards [data-action-id="statistics.open-stage"]').filter({ hasText: '报价' }).click();
    else await sales.locator('tr[data-stage="L5_QUOTATION"] [data-action-id="statistics.open-stage"]').click();
    await expect(sales).toHaveURL(/\/projects\?stage=L5_QUOTATION&q=/);
    const list = (await api(sales, '/api/v1/projects?stage=L5_QUOTATION&q=' + encodeURIComponent(tag))).body;
    expect(list.total).toBe(4);
    await expect(sales.getByRole('heading', { level: 1 })).toContainText('4');
    await sales.context().close();
  });
}

test('G3-3B 统计四角色范围与跨组金额不外泄', async ({ browser }, info) => {
  test.skip(info.project.name !== 'desktop', 'once');
  const vp = { width: 1440, height: 900 };
  const tag = uniq('G33B角色');
  const a = await session(browser, 'synthetic-a', vp);
  const c = await session(browser, 'synthetic-c', vp);
  await create(a, `${tag}甲`, 'L5_QUOTATION', 1000.1);
  await create(c, `${tag}丙`, 'L5_QUOTATION', 654321.09);
  const want: Record<string, string> = { 'synthetic-a': '¥1,000.10', 'synthetic-b': '¥0.00', 'synthetic-manager': '¥1,000.10', 'synthetic-manager-other': '¥654,321.09', 'synthetic-director': '¥655,321.19' };
  for (const [subject, total] of Object.entries(want)) {
    const p = subject === 'synthetic-a' ? a : await session(browser, subject, vp);
    let raw = '';
    p.on('response', async (r) => {
      if (r.url().includes('/api/v1/stage-summary')) raw += await r.text();
    });
    await p.goto('/statistics');
    await p.getByRole('textbox', { name: '搜索报备' }).fill(tag);
    await expect(p.getByTestId('stat-known-total')).toHaveText(total);
    if (subject === 'synthetic-manager') {
      expect(raw).not.toContain('654321');
      expect(raw).not.toContain('655321');
      await p.evaluate(() => scrollTo(0, 0));
      await p.screenshot({ path: shot('statistics-manager-1440'), fullPage: true });
    }
    if (subject === 'synthetic-director') await p.screenshot({ path: shot('statistics-director-1440'), fullPage: true });
    await p.context().close();
  }
  await c.context().close();
});

test('G3-3A 遗留：加载更多没有新项目时停止并提示刷新', async ({ browser }, info) => {
  test.skip(info.project.name !== 'desktop', 'once');
  const p = await session(browser, 'synthetic-a', { width: 1440, height: 900 });
  // Column L0 reports 25 projects but every later page repeats the first page
  // (projects moved/deleted concurrently). Controlled fake for determinism.
  const items = Array.from({ length: 10 }, (_, i) => ({
    id: `00000000-0000-4000-8000-${String(i).padStart(12, '0')}`, registrationNo: `BB-R3-${i}`, projectName: `R3重复${i}`, customer: 'R3', intakeStatus: 'DRAFT', salesStage: 'L0_LEAD', ownerSubject: 'synthetic-a', nextAction: '', nextFollowupAt: null, actions: {},
  }));
  let pages = 0;
  await p.route('**/api/v1/projects?*stage=L0_LEAD*', (route: Route) => {
    pages++;
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items, total: 25, asOf: new Date().toISOString() }) });
  });
  await p.goto('/pipeline');
  const col = p.locator('section.stage-column[data-stage="L0_LEAD"]');
  await expect(col.locator('.stage-card')).toHaveCount(10);
  await col.locator('[data-action-id="pipeline.load-more"]').click();
  await expect(col.getByRole('status')).toContainText('继续加载没有新项目');
  await expect(col.locator('[data-action-id="pipeline.load-more"]')).toHaveCount(0);
  expect(pages).toBe(2); // no automatic retries over empty pages
  await col.locator('[data-action-id="pipeline.column-refresh"]').click();
  await expect(col.locator('.stage-card')).toHaveCount(10);
  expect(pages).toBe(3);
  await p.context().close();
});

test('G3-3A 遗留：409 后最新阶段已变，目标阶段需重新确认', async ({ browser, cleanup }, info) => {
  test.skip(info.project.name !== 'desktop', 'once');
  const p = await session(browser, 'synthetic-a', { width: 1440, height: 900 });
  const tag = uniq('G33B重选');
  const proj = await create(p, tag, 'L2_TECHNICAL', null);
  cleanup.track(proj.id); // G3-4-A1: returned after the test, pass or fail
  await p.goto('/pipeline');
  await p.getByRole('textbox', { name: '搜索报备' }).fill(tag);
  const l2 = p.locator('section.stage-column[data-stage="L2_TECHNICAL"]');
  await l2.evaluate((el) => (el.parentElement!.scrollLeft = (el as HTMLElement).offsetLeft - el.parentElement!.offsetLeft));
  await expect(l2.locator('.stage-card')).toHaveCount(1);
  await l2.locator('[data-action-id="pipeline.stage-change"]').click();
  const dialog = p.getByRole('dialog');
  await dialog.locator('select[name=targetStage]').selectOption('L3_SITE_SURVEY');
  await dialog.locator('[name=stageReason]').fill('合成：推进到现场勘察');
  // Another session moves the project to L5 first (draft: any stage allowed).
  const cur = (await api(p, `/api/v1/projects/${proj.id}`)).body;
  expect((await api(p, `/api/v1/projects/${proj.id}/stage-change`, 'POST', { version: cur.version, salesStage: 'L5_QUOTATION', reason: '另一会话' })).status).toBe(200);
  await action(p, 'pipeline.stage-confirm').click();
  await expect(dialog.getByRole('alert').first()).toContainText('项目已被其他人更新');
  await expect(dialog).toContainText('当前阶段：报价');
  // L3 is still legal for a draft, so it stays selected; the reason is kept.
  await expect(dialog.locator('[name=stageReason]')).toHaveValue('合成：推进到现场勘察');
  await expect(dialog.locator('select[name=targetStage]')).toHaveValue('L3_SITE_SURVEY');
  // Now submit it in L5 and have another session move it to HOLD: from HOLD
  // only L1 is legal, so the previous target must be cleared.
  await action(p, 'dialog.cancel').click();
  await action(p, 'guard.leave').click();
  const draft = (await api(p, `/api/v1/projects/${proj.id}`)).body;
  const filled = { projectName: tag, customer: tag + '客户', location: { province: '合成省', city: '合成市', factoryName: tag, workshop: '一车间' }, projectType: 'RETROFIT', salesStage: 'L5_QUOTATION', informationSource: 'END_CUSTOMER', confidenceLevel: 'CUSTOMER_CONFIRMED', requirements: ['ANTI_SWAY'], requirementNotes: '合成需求', plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 2 }] };
  const patched = (await api(p, `/api/v1/projects/${proj.id}`, 'PATCH', { ...filled, version: draft.version })).body;
  const submitted = (await api(p, `/api/v1/projects/${proj.id}/submit`, 'POST', { version: patched.version })).body;
  try {
    await p.getByRole('textbox', { name: '搜索报备' }).fill(tag + ' ');
    await p.getByRole('textbox', { name: '搜索报备' }).fill(tag);
    const l5 = p.locator('section.stage-column[data-stage="L5_QUOTATION"]');
    await l5.evaluate((el) => (el.parentElement!.scrollLeft = (el as HTMLElement).offsetLeft - el.parentElement!.offsetLeft));
    await expect(l5.locator('.stage-card')).toHaveCount(1);
    await l5.locator('[data-action-id="pipeline.stage-change"]').click();
    await dialog.locator('select[name=targetStage]').selectOption('L6_TENDER');
    await dialog.locator('[name=stageReason]').fill('合成：推进到招投标');
    expect((await api(p, `/api/v1/projects/${proj.id}/stage-change`, 'POST', { version: submitted.version, salesStage: 'HOLD', reason: '另一会话暂缓' })).status).toBe(200);
    await action(p, 'pipeline.stage-confirm').click();
    await expect(dialog.getByRole('alert').first()).toContainText('请重新选择目标阶段');
    await expect(dialog.locator('select[name=targetStage]')).toHaveValue('');
    await expect(action(p, 'pipeline.stage-confirm')).toBeDisabled();
    expect(await dialog.locator('select[name=targetStage] option').allTextContents()).toEqual(['请选择', '需求确认']);
    await expect(dialog.locator('[name=stageReason]')).toHaveValue('合成：推进到招投标');
    expect((await api(p, `/api/v1/projects/${proj.id}`)).body.salesStage).toBe('HOLD');
  } finally {
    await p.context().close();
  }
});
