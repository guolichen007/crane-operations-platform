import { fileURLToPath } from 'node:url';
import {
  test,
  expect,
  type Browser,
  type Page,
  type Route,
} from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
// G3-3A: real sales-stage pipeline against the real API. Counts come from
// /stage-summary, each column pages through /projects?stage. The shared e2e
// database accumulates projects from other specs, so assertions compare the
// UI with the API in the same data state instead of fixed numbers.
const action = (p: Page, id: string) => p.locator(`[data-action-id="${id}"]:visible`);
const shot = (name: string) => fileURLToPath(new URL(`../../../local-private/baobei/g33a-evidence/${name}.png`, import.meta.url));
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
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': me.csrfToken, 'Idempotency-Key': 'g33a-' + crypto.randomUUID() },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      return { status: r.status, body: await r.json() };
    },
    { path, method, body }
  );
}
async function create(p: Page, name: string, stage: string, extra: Record<string, unknown> = {}) {
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
    plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 2 }],
    ...extra,
  });
  expect(r.status).toBe(200);
  return r.body;
}
const column = (p: Page, code: string) => p.locator(`section.stage-column[data-stage="${code}"]`);
// Bring a column into view by scrolling the board horizontally only, so the
// page header (scope, asOf, filters) stays visible in screenshots.
const reveal = (p: Page, code: string) =>
  column(p, code).evaluate((el) => {
    const board = el.parentElement!;
    board.scrollLeft = (el as HTMLElement).offsetLeft - board.offsetLeft - 8;
  });
const noOverflow = (p: Page) => p.evaluate(() => document.documentElement.scrollWidth <= innerWidth);

for (const vp of [
  { name: '1440', width: 1440, height: 900 },
  { name: '1366', width: 1366, height: 768 },
  { name: '768', width: 768, height: 1024 },
  { name: '390', width: 390, height: 844 },
]) {
  test(`G3-3A 管道真实统计、逐栏分页与阶段推进 @${vp.name}`, async ({ browser }, info) => {
    test.skip(info.project.name !== 'desktop', 'viewports are set explicitly; run once');
    const tag = `G33A${vp.name}-${Date.now()}`;
    const sales = await session(browser, 'synthetic-a', vp);
    // 23 projects in L6 (more than two pages of 10), a long name, follow-up
    // states, and one project to advance.
    const base = (await api(sales, '/api/v1/projects?stage=L6_TENDER&pageSize=1')).body.total as number;
    for (let i = 0; i < 23; i++) await create(sales, `${tag}招投标${String(i).padStart(2, '0')}`, 'L6_TENDER');
    const long = await create(sales, `${tag}超长名称${'行车自动化改造与智能调度系统一期工程'.repeat(4)}`, 'L2_TECHNICAL', { customer: '超长客户名称'.repeat(12) + tag });
    const moving = await create(sales, `${tag}推进`, 'L2_TECHNICAL');
    const submitted = (await api(sales, `/api/v1/projects/${moving.id}/submit`, 'POST', { version: moving.version })).body;
    // G3-3B: the submitted fixture is returned in finally, also when an
    // assertion below fails, so it never stays in team one's review queue.
    try {
    expect(submitted.intakeStatus).toMatch(/SUBMITTED|UNDER_REVIEW/);
    const day = 86_400_000;
    for (const [p, at] of [[long, new Date(Date.now() - day).toISOString()], [submitted, new Date(Date.now() + 3 * day).toISOString()]] as const) {
      const cur = (await api(sales, `/api/v1/projects/${p.id}`)).body;
      if (cur.intakeStatus === 'DRAFT') {
        const input = Object.fromEntries(['projectName', 'customer', 'location', 'salesStage', 'projectType', 'informationSource', 'confidenceLevel', 'requirements', 'requirementNotes', 'plannedEquipment'].map((k) => [k, cur[k]]));
        const r = await api(sales, `/api/v1/projects/${p.id}`, 'PATCH', { ...input, version: cur.version, nextAction: '合成下一步', nextFollowupAt: at });
        expect(r.status).toBe(200);
      } else {
        const r = await api(sales, `/api/v1/projects/${p.id}/activities`, 'POST', { version: cur.version, type: 'PHONE', occurredAt: new Date().toISOString(), summary: '合成', result: '', nextAction: '合成下一步', nextFollowupAt: at });
        expect(r.status).toBe(200);
      }
    }

    await sales.goto('/pipeline');
    const summary = (await api(sales, '/api/v1/stage-summary')).body;
    expect(summary.stages.map((s: { code: string }) => s.code)).toHaveLength(12);
    expect(summary.stages.find((s: { code: string }) => s.code === 'L6_TENDER').count).toBe(base + 23);
    await expect(sales.getByRole('heading', { name: /项目管道/ })).toContainText(String(summary.total));
    const mobile = vp.width <= 700;
    if (mobile) {
      // One stage at a time; the picker shows every stage with its real count.
      const picker = sales.getByRole('combobox', { name: '选择销售阶段' });
      for (const s of summary.stages) await expect(picker.locator(`option[value="${s.code}"]`)).toContainText(`（${s.count}）`);
      await picker.selectOption('L6_TENDER');
    } else {
      // The stage strip shows every stage with its real count and jumps to a column.
      const strip = sales.getByRole('navigation', { name: '阶段分布' }).locator('[data-action-id="pipeline.jump"]');
      await expect(strip).toHaveCount(12);
      for (const [i, s] of summary.stages.entries()) await expect(strip.nth(i)).toContainText(String(s.count));
      await strip.filter({ hasText: '招投标' }).click();
      await expect(column(sales, 'L6_TENDER')).toBeInViewport();
    }
    const l6 = column(sales, 'L6_TENDER');
    await expect(l6.locator('.stage-count')).toHaveText(String(base + 23));
    await expect(l6.locator('.stage-loaded')).toHaveText(`已加载 10 / 共 ${base + 23}`);
    await expect(l6.locator('.stage-card')).toHaveCount(10);
    // Load more in L6 only; the L2 column keeps its own state.
    if (!mobile) {
      await reveal(sales, 'L2_TECHNICAL');
      await expect(column(sales, 'L2_TECHNICAL').locator('.stage-loaded')).toContainText('已加载');
      await reveal(sales, 'L6_TENDER');
    }
    const l2Before = mobile ? '' : await column(sales, 'L2_TECHNICAL').locator('.stage-loaded').textContent();
    await l6.locator('[data-action-id="pipeline.load-more"]').click();
    await expect(l6.locator('.stage-card')).toHaveCount(20);
    await expect(l6.locator('.stage-loaded')).toHaveText(`已加载 20 / 共 ${base + 23}`);
    if (!mobile) await expect(column(sales, 'L2_TECHNICAL').locator('.stage-loaded')).toHaveText(l2Before!);
    expect(await noOverflow(sales)).toBe(true);
    await sales.evaluate(() => scrollTo(0, 0));
    await sales.screenshot({ path: shot(`pipeline-sales-${vp.name}`), fullPage: false });

    // Filter: all columns reset to page 1 and use the same base filter as the summary.
    await sales.getByRole('textbox', { name: '搜索报备' }).fill(tag);
    const filtered = (await api(sales, '/api/v1/stage-summary?q=' + encodeURIComponent(tag))).body;
    await expect(sales.getByRole('heading', { name: /项目管道/ })).toContainText(String(filtered.total));
    await expect(l6.locator('.stage-count')).toHaveText('23');
    await expect(sales.locator('.pipeline-drift')).toHaveCount(0);
    await expect(l6.locator('.stage-loaded')).toHaveText('已加载 10 / 共 23');
    if (mobile) await sales.getByRole('combobox', { name: '选择销售阶段' }).selectOption('L2_TECHNICAL');
    else await reveal(sales, 'L2_TECHNICAL');
    const l2 = column(sales, 'L2_TECHNICAL');
    await expect(l2.locator('.stage-count')).toHaveText('2');
    // Follow-up marks come from nextFollowupAt only.
    await expect(l2.locator('.stage-card').filter({ hasText: '超长名称' })).toContainText('已逾期');
    await expect(l2.locator('.stage-card').filter({ hasText: `${tag}推进` })).toContainText('已安排');
    await expect(l6.locator('.stage-card').first()).toContainText('未安排下一次跟进');
    expect(await noOverflow(sales)).toBe(true);
    const longCard = await l2.locator('.stage-card').filter({ hasText: '超长名称' }).boundingBox();
    const colBox = await l2.boundingBox();
    expect(longCard!.width).toBeLessThanOrEqual(colBox!.width);
    await sales.evaluate(() => scrollTo(0, 0));
    await sales.screenshot({ path: shot(`pipeline-filtered-${vp.name}`), fullPage: false });

    // Advance a stage through the real stage-change API.
    const card = l2.locator('.stage-card').filter({ hasText: `${tag}推进` });
    await card.locator('[data-action-id="pipeline.stage-change"]').click();
    const dialog = sales.getByRole('dialog');
    await expect(dialog).toContainText('当前阶段：技术交流');
    const options = await dialog.locator('select[name=targetStage] option').allTextContents();
    expect(options).toEqual(['请选择', '现场勘察', '暂缓', '已失单']);
    await expect(action(sales, 'pipeline.stage-confirm')).toBeDisabled();
    // Another session changes the project first: stale version → 409, reason kept, card stays.
    const cur = (await api(sales, `/api/v1/projects/${moving.id}`)).body;
    expect((await api(sales, `/api/v1/projects/${moving.id}/activities`, 'POST', { version: cur.version, type: 'PHONE', occurredAt: new Date().toISOString(), summary: '另一会话', result: '', nextAction: '合成下一步', nextFollowupAt: new Date(Date.now() + 3 * day).toISOString() })).status).toBe(200);
    await dialog.locator('select[name=targetStage]').selectOption('L3_SITE_SURVEY');
    await dialog.locator('[name=stageReason]').fill('完成技术交流，安排现场勘察');
    await action(sales, 'pipeline.stage-confirm').click();
    await expect(dialog.getByRole('alert')).toContainText('项目已被其他人更新');
    await expect(dialog.locator('[name=stageReason]')).toHaveValue('完成技术交流，安排现场勘察');
    expect((await api(sales, `/api/v1/projects/${moving.id}`)).body.salesStage).toBe('L2_TECHNICAL');
    // The dialog re-read the latest version; confirming now succeeds once.
    await dialog.locator('select[name=targetStage]').selectOption('L3_SITE_SURVEY');
    await action(sales, 'pipeline.stage-confirm').click();
    await expect(dialog).toHaveCount(0);
    await expect(l2.locator('.stage-count')).toHaveText('1');
    if (mobile) await sales.getByRole('combobox', { name: '选择销售阶段' }).selectOption('L3_SITE_SURVEY');
    else await reveal(sales, 'L3_SITE_SURVEY');
    await expect(column(sales, 'L3_SITE_SURVEY').locator('.stage-count')).toHaveText('1');
    await expect(column(sales, 'L3_SITE_SURVEY').locator('.stage-card')).toContainText(`${tag}推进`);
    const audit = (await api(sales, `/api/v1/projects/${moving.id}`)).body.audit.filter((a: { action: string }) => a.action === 'project.stage-change');
    expect(audit).toHaveLength(1);
    } finally {
      // Leave the shared e2e database as found (other specs open queue page 1).
      const director = await session(browser, 'synthetic-director', { width: 1440, height: 900 });
      const latest = (await api(director, `/api/v1/projects/${moving.id}`)).body;
      if (['SUBMITTED', 'UNDER_REVIEW'].includes(latest.intakeStatus))
        expect((await api(director, `/api/v1/projects/${moving.id}/return`, 'POST', { version: latest.version, reason: '合成测试数据清理' })).status).toBe(200);
      await director.context().close();
      await sales.context().close();
    }
  });
}

test('G3-3A 四角色范围：主管本组、另一组主管、总监与管理员', async ({ browser }, info) => {
  test.skip(info.project.name !== 'desktop', 'role scope once');
  const vp = { width: 1440, height: 900 };
  const tag = `G33A角色-${Date.now()}`;
  const a = await session(browser, 'synthetic-a', vp);
  const c = await session(browser, 'synthetic-c', vp);
  await create(a, `${tag}甲`, 'L5_QUOTATION');
  await create(c, `${tag}丙`, 'L5_QUOTATION');
  const admin = await session(browser, 'synthetic-admin', vp);
  expect((await api(admin, '/api/v1/projects', 'POST', { projectName: `${tag}待分配`, customer: '合成', salesStage: 'L5_QUOTATION' })).status).toBe(200);
  const want: Record<string, number> = { 'synthetic-a': 1, 'synthetic-b': 0, 'synthetic-manager': 1, 'synthetic-manager-other': 1, 'synthetic-director': 2, 'synthetic-admin': 3 };
  for (const [subject, n] of Object.entries(want)) {
    const p = subject === 'synthetic-admin' ? admin : subject === 'synthetic-a' ? a : await session(browser, subject, vp);
    await p.goto('/pipeline');
    await p.getByRole('textbox', { name: '搜索报备' }).fill(tag);
    const sum = (await api(p, '/api/v1/stage-summary?q=' + encodeURIComponent(tag))).body;
    expect(sum.total, subject).toBe(n);
    // Heading, strip and columns all show the filtered state (no stale counts).
    await expect(p.getByRole('heading', { name: /项目管道/ })).toContainText(String(n));
    await expect(p.getByRole('navigation', { name: '阶段分布' }).locator('[data-action-id="pipeline.jump"]').filter({ hasText: '招投标' })).toContainText('0');
    await expect(p.locator('.pipeline-drift')).toHaveCount(0);
    const l5 = column(p, 'L5_QUOTATION');
    await reveal(p, 'L5_QUOTATION');
    await expect(l5.locator('.stage-count')).toHaveText(String(n));
    await expect(l5.locator('.stage-card')).toHaveCount(n);
    if (subject === 'synthetic-admin') await expect(l5.locator('.stage-card .unassigned')).toHaveText(['待分配']);
    if (subject === 'synthetic-manager') await p.screenshot({ path: shot('pipeline-manager-1440') });
    if (subject === 'synthetic-director') await p.screenshot({ path: shot('pipeline-director-1440') });
    await p.context().close();
  }
  await c.context().close();
});

test('G3-3A 快速切换筛选时旧响应不覆盖新结果，栏错误可重试', async ({ browser }, info) => {
  test.skip(info.project.name !== 'desktop', 'once');
  const vp = { width: 1440, height: 900 };
  const tag = `G33A竞态-${Date.now()}`;
  const p = await session(browser, 'synthetic-a', vp);
  await create(p, `${tag}一`, 'L0_LEAD');
  await p.goto('/pipeline');
  const l0 = column(p, 'L0_LEAD');
  await expect(l0.locator('.stage-loaded')).toContainText('已加载');
  // Slow down the first filter's requests; the second filter answers first.
  await p.route('**/api/v1/**', async (route: Route) => {
    const u = route.request().url();
    if (u.includes('q=' + encodeURIComponent('不存在的过滤词'))) await new Promise((r) => setTimeout(r, 1500));
    await route.continue();
  });
  const search = p.getByRole('textbox', { name: '搜索报备' });
  const stale = p.waitForResponse((r) => r.url().includes('stage-summary') && r.url().includes(encodeURIComponent('不存在的过滤词')));
  await search.fill('不存在的过滤词');
  await p.waitForRequest((r) => r.url().includes('stage-summary') && r.url().includes(encodeURIComponent('不存在的过滤词')));
  await search.fill(tag);
  await expect(l0.locator('.stage-count')).toHaveText('1');
  await expect(l0.locator('.stage-card')).toHaveCount(1);
  // The stale (empty) summary arrives after the current one rendered; it must be discarded.
  await stale;
  await p.evaluate(() => new Promise(requestAnimationFrame));
  await expect(l0.locator('.stage-count')).toHaveText('1');
  await expect(l0.locator('.stage-card')).toHaveCount(1);
  // A failing column shows its own error and retries without touching others.
  await p.unroute('**/api/v1/**');
  let fail = true;
  await p.route('**/api/v1/projects?*stage=L0_LEAD*', (route: Route) => (fail ? route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ code: 'SERVICE_UNAVAILABLE', message: '服务暂时不可用' }) }) : route.continue()));
  await action(p, 'pipeline.refresh').first().click();
  await expect(l0.getByRole('alert')).toContainText('服务暂时不可用');
  fail = false;
  await l0.locator('[data-action-id="pipeline.column-retry"]').click();
  await expect(l0.locator('.stage-card')).toHaveCount(1);
  // Unknown filter on the API is a clear 400, not ignored.
  expect((await api(p, '/api/v1/stage-summary?stage=L0_LEAD')).status).toBe(400);
  expect((await api(p, '/api/v1/projects?stage=L9')).status).toBe(400);
  await p.context().close();
});
