import { fileURLToPath } from 'node:url';
import {
  test,
  expect,
  type Browser,
  type Page,
  type Route,
} from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
// G3-4-A0 gate 2: four-role home against the real API. Every number on the
// home page must equal the server aggregate in the same data state, and the
// list it opens must report the same total. The shared e2e database
// accumulates projects from other specs, so assertions compare UI ↔ API ↔ list
// rather than fixed numbers; scope/privacy is proven with deltas.
const action = (p: Page, id: string) => p.locator(`[data-action-id="${id}"]:visible`);
const shot = (name: string) => fileURLToPath(new URL(`../../../local-private/baobei/g34a0-evidence/${name}.png`, import.meta.url));
const viewports = [
  { name: '1440', width: 1440, height: 900 },
  { name: '1366', width: 1366, height: 768 },
  { name: '768', width: 768, height: 1024 },
  { name: '390', width: 390, height: 844 },
];
const roles = [
  { key: 'sales', subject: 'synthetic-a' },
  { key: 'manager', subject: 'synthetic-manager' },
  { key: 'director', subject: 'synthetic-director' },
  { key: 'admin', subject: 'synthetic-admin' },
];
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
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': me.csrfToken, 'Idempotency-Key': 'g34a0-' + crypto.randomUUID() },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      return { status: r.status, body: await r.json() };
    },
    { path, method, body }
  );
}
const tile = (p: Page, label: string) => action(p, 'dashboard.task').filter({ hasText: label });
const noOverflow = (p: Page) => p.evaluate(() => document.documentElement.scrollWidth <= innerWidth);
const cents = (s: string) => BigInt(s.replace('.', ''));
const yuan = (s: string) => {
  const [i, f] = s.split('.');
  return '¥' + i.replace(/\B(?=(\d{3})+(?!\d))/g, ',') + '.' + f;
};
async function ready(p: Page) {
  await expect(p.locator('.task-board')).toBeVisible();
  await expect(p.locator('.stage-bars')).toBeVisible();
}
// The tile → list mapping the home page promises; each entry is the list API
// with the same filter as the count.
const listFor: Record<string, { label: string; count: (c: Record<string, number>) => number; api: string; url: RegExp }> = {
  overdue: { label: '已逾期跟进', count: (c) => c.overdue, api: '/api/v1/projects?followup=overdue&pageSize=1', url: /\/followups\?window=overdue$/ },
  upcoming7d: { label: '未来 7 天跟进', count: (c) => c.upcoming7d, api: '/api/v1/projects?followup=upcoming&pageSize=1', url: /\/followups\?window=upcoming$/ },
  returned: { label: '退回补充', count: (c) => c.returned, api: '/api/v1/projects?status=RETURNED&pageSize=1', url: /\/projects\?status=RETURNED$/ },
  drafts: { label: '草稿', count: (c) => c.drafts, api: '/api/v1/projects?status=DRAFT&pageSize=1', url: /\/projects\?status=DRAFT$/ },
  reviewable: { label: '你可独立审核', count: (c) => c.reviewable, api: '/api/v1/reviews?kind=pending&eligibility=reviewable&pageSize=1', url: /\/reviews\?eligibility=reviewable$/ },
  blocked: { label: '需要其他人处理', count: (c) => c.reviewQueue - c.reviewable, api: '/api/v1/reviews?kind=pending&eligibility=blocked&pageSize=1', url: /\/reviews\?eligibility=blocked$/ },
  unassignedDrafts: { label: '待分配草稿', count: (c) => c.unassignedDrafts, api: '/api/v1/projects?assignment=unassigned&pageSize=1', url: /\/unassigned$/ },
};
const expectedTiles: Record<string, string[]> = {
  sales: ['overdue', 'upcoming7d', 'returned', 'drafts'],
  manager: ['reviewable', 'blocked', 'overdue', 'upcoming7d'],
  director: ['reviewable', 'blocked', 'overdue', 'upcoming7d'],
  admin: ['unassignedDrafts', 'reviewable', 'blocked', 'overdue'],
};

// Realistic synthetic data so every role's home has content: sales A
// (team of synthetic-manager) gets projects across stages, overdue and
// upcoming follow-ups, two submitted filings and one returned; admin gets an
// unassigned draft. Submitted filings are returned by the director in the
// last test so the shared review queue is left as other specs expect.
const SEED = 'A0示例';
const full = (name: string, stage: string, extra: Record<string, unknown> = {}) => ({
  projectName: name,
  customer: name.replace(/项目.*$/, '') + '有限公司',
  location: { province: '合成省', city: '合成市', factoryName: name + '厂区', workshop: '主厂房' },
  projectType: 'RETROFIT',
  salesStage: stage,
  informationSource: 'END_CUSTOMER',
  confidenceLevel: 'CUSTOMER_CONFIRMED',
  requirements: ['ANTI_SWAY'],
  requirementNotes: '合成需求：桥机防摇与定位',
  plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 2, spanM: 22.5 }],
  ...extra,
});
const day = 24 * 3600 * 1000;
async function seed(browser: Browser) {
  const a = await session(browser, 'synthetic-a', viewports[0]);
  const exists = (await api(a, '/api/v1/projects?pageSize=1&q=' + encodeURIComponent(SEED))).body.total;
  if (exists) return a.context().close();
  const k = (v: number | null) => (v === null ? { estimatedAmount: null } : { estimatedAmount: v, knowledge: { estimatedAmount: 'KNOWN' } });
  const plan: [string, string, Record<string, unknown>][] = [
    [`${SEED}·东港钢铁项目一期桥机改造`, 'L1_REQUIREMENT', { ...k(860000), nextFollowupAt: new Date(Date.now() - 2 * day).toISOString(), nextAction: '电话确认现场停机窗口' }],
    [`${SEED}·北岭重工项目铸造车间`, 'L2_TECHNICAL', { ...k(null), nextFollowupAt: new Date(Date.now() + 2 * day).toISOString(), nextAction: '发送技术交流资料' }],
    [`${SEED}·西川港务项目岸桥`, 'L3_SITE_SURVEY', { ...k(1250000.5), nextFollowupAt: new Date(Date.now() + 5 * day).toISOString(), nextAction: '安排现场勘察' }],
    [`${SEED}·南湖造纸项目`, 'L4_SOLUTION', { estimatedAmount: 0 }],
    [`${SEED}·中原电厂项目检修吊`, 'L5_QUOTATION', { ...k(3480000), nextFollowupAt: new Date(Date.now() - 1 * day).toISOString(), nextAction: '报价复核后回电' }],
    [`${SEED}·临江水泥项目`, 'L6_TENDER', k(5200000)],
  ];
  for (const [name, stage, extra] of plan) expect((await api(a, '/api/v1/projects', 'POST', full(name, stage, extra))).status).toBe(200);
  const submitted: string[] = [];
  for (const [name, stage, amount] of [[`${SEED}·华北冶金项目连铸跨`, 'L5_QUOTATION', 2160000], [`${SEED}·东方化工项目原料库`, 'L2_TECHNICAL', 640000], [`${SEED}·远山物流项目仓储`, 'L1_REQUIREMENT', null]] as const) {
    const c = await api(a, '/api/v1/projects', 'POST', full(name, stage, k(amount)));
    expect(c.status).toBe(200);
    const sub = await api(a, `/api/v1/projects/${c.body.id}/submit`, 'POST', { version: c.body.version });
    expect(sub.status).toBe(200);
    submitted.push(c.body.id);
  }
  const m = await session(browser, 'synthetic-manager', viewports[0]);
  const last = (await api(m, `/api/v1/projects/${submitted[2]}`)).body;
  expect((await api(m, `/api/v1/projects/${submitted[2]}/return`, 'POST', { version: last.version, reason: '合成：请补充现场照片' })).status).toBe(200);
  const admin = await session(browser, 'synthetic-admin', viewports[0]);
  expect((await api(admin, '/api/v1/projects', 'POST', { projectName: `${SEED}·待分配·青石矿业项目`, customer: '青石矿业有限公司', ...k(450000), version: 1 })).status).toBe(200);
  for (const p of [a, m, admin]) await p.context().close();
}

for (const vp of viewports) {
  test(`G3-4-A0 四角色工作台：数字=接口=列表 @${vp.name}`, async ({ browser }, info) => {
    test.skip(info.project.name !== 'desktop', 'viewports are set explicitly; run once');
    test.setTimeout(150000);
    await seed(browser);
    for (const role of roles) {
      const p = await session(browser, role.subject, vp);
      await ready(p);
      const s = (await api(p, '/api/v1/workspace-summary')).body;
      const g = (await api(p, '/api/v1/stage-summary')).body;
      // Tiles: order, numbers and the same-filter list totals.
      const labels = await action(p, 'dashboard.task').locator('.task-label').allTextContents();
      expect(labels).toEqual(expectedTiles[role.key].map((k) => listFor[k].label));
      for (const k of expectedTiles[role.key]) {
        const want = listFor[k].count(s.counts);
        await expect(tile(p, listFor[k].label).locator('.task-count')).toHaveText(String(want));
        const list = await api(p, listFor[k].api);
        expect(list.status, k).toBe(200);
        expect(list.body.total, `${role.key} ${k}`).toBe(want);
      }
      await expect(p.locator('.scope-line')).toContainText('共 ' + s.counts.total + ' 项');
      await expect(p.locator('.scope-line')).toContainText('统计时间');
      // Stage distribution = stage-summary; the grand total = workspace total
      // (both use projectScope over all intake statuses).
      expect(g.total).toBe(s.counts.total);
      await expect(action(p, 'dashboard.stage')).toHaveCount(12);
      const bars = await action(p, 'dashboard.stage').locator('.stage-bar-count').allTextContents();
      expect(bars.map(Number)).toEqual(g.stages.map((x: { count: number }) => x.count));
      await expect(p.getByTestId('home-known-total')).toHaveText(yuan(g.amount.knownTotal));
      await expect(p.getByTestId('home-unknown')).toHaveText(String(g.amount.unknownCount));
      await expect(p.getByTestId('home-legacy')).toHaveText(String(g.amount.legacyUnconfirmedCount));
      await expect(p.getByTestId('home-invalid')).toHaveText(String(g.amount.invalidCount));
      await expect(p.locator('.home-amount')).toContainText('不是合同金额、收入或回款');
      // No revenue / collection / YoY / win-rate figures anywhere (the amount
      // definition sentence that says "not revenue" is excluded).
      const text = await p.locator('main').evaluate((m) => {
        const c = m.cloneNode(true) as HTMLElement;
        c.querySelectorAll('.panel-def').forEach((x) => x.remove());
        return c.innerText;
      });
      expect(text).not.toMatch(/收入|回款|同比|环比|赢率|转化率|成交率/);
      // Role-specific absence.
      if (role.key === 'sales') await expect(tile(p, '你可独立审核')).toHaveCount(0);
      if (role.key !== 'admin') await expect(tile(p, '待分配草稿')).toHaveCount(0);
      if (role.key === 'sales') await expect(p.locator('.home-governance')).toHaveCount(0);
      else await expect(p.locator('.home-governance [data-action-id="nav.open"]').first()).toBeVisible();
      expect(await noOverflow(p), `${role.key} overflow @${vp.name}`).toBe(true);
      await expect(p.locator('.toast')).toHaveCount(0, { timeout: 8000 }); // login toast must not cover evidence
      await p.evaluate(() => scrollTo(0, 0));
      await p.screenshot({ path: shot(`${role.key}-${vp.name}`), fullPage: true });
      // Click-through at one desktop width (lists are covered by API above).
      if (vp.name === '1440') {
        for (const k of expectedTiles[role.key]) {
          await tile(p, listFor[k].label).click();
          await expect(p).toHaveURL(listFor[k].url);
          if (k === 'reviewable' || k === 'blocked') {
            const view = action(p, 'review.view').filter({ hasText: k === 'reviewable' ? '我可审核' : '需要其他人处理' });
            await expect(view).toHaveAttribute('aria-selected', 'true');
            await expect(view).toContainText(String(listFor[k].count(s.counts)));
          } else {
            await expect(p.locator('h1 small')).toHaveText(String(listFor[k].count(s.counts)));
          }
          await p.goto('/');
          await ready(p);
        }
        const first = g.stages.find((x: { count: number }) => x.count > 0);
        if (first) {
          await action(p, 'dashboard.stage').nth(g.stages.indexOf(first)).click();
          await expect(p).toHaveURL(/\/projects\?stage=/);
          const code = new URL(p.url()).searchParams.get('stage');
          const st = g.stages.find((x: { code: string }) => x.code === code);
          await expect(p.locator('h1 small')).toHaveText(String(st.count));
          expect((await api(p, `/api/v1/projects?stage=${code}&pageSize=1`)).body.total).toBe(st.count);
        }
        // Zero stages cannot be clicked into an empty list.
        const zero = g.stages.find((x: { count: number }) => x.count === 0);
        if (zero) {
          await p.goto('/');
          await ready(p);
          await expect(action(p, 'dashboard.stage').nth(g.stages.indexOf(zero))).toBeDisabled();
        }
      }
      await p.context().close();
    }
  });
}

test('G3-4-A0 范围与隐私：跨组金额与待分配草稿不外泄', async ({ browser }, info) => {
  test.skip(info.project.name !== 'desktop', 'once');
  test.setTimeout(120000);
  const vp = { width: 1440, height: 900 };
  const tag = `A0隐私-${Date.now()}`;
  const subjects = ['synthetic-a', 'synthetic-manager', 'synthetic-manager-other', 'synthetic-director', 'synthetic-admin'];
  const pages: Record<string, Page> = {};
  for (const s of subjects) pages[s] = await session(browser, s, vp);
  const snap = async () => {
    const out: Record<string, { total: number; known: bigint; unassigned: number }> = {};
    for (const s of subjects) {
      const w = (await api(pages[s], '/api/v1/workspace-summary')).body;
      const g = (await api(pages[s], '/api/v1/stage-summary')).body;
      out[s] = { total: w.counts.total, known: cents(g.amount.knownTotal), unassigned: w.counts.unassignedDrafts };
    }
    return out;
  };
  const before = await snap();
  // Other team (synthetic-c → synthetic-manager-other) with a distinctive amount.
  const c = await session(browser, 'synthetic-c', vp);
  const other = await api(c, '/api/v1/projects', 'POST', { projectName: tag + '丙组', customer: tag, estimatedAmount: 7654321.07, knowledge: { estimatedAmount: 'KNOWN' }, version: 1 });
  expect(other.status).toBe(200);
  // Admin creates an unassigned draft with an amount.
  const un = await api(pages['synthetic-admin'], '/api/v1/projects', 'POST', { projectName: tag + '待分配', customer: tag, estimatedAmount: 1111.11, knowledge: { estimatedAmount: 'KNOWN' }, version: 1 });
  expect(un.status).toBe(200);
  const after = await snap();
  const d = (s: string) => ({ total: after[s].total - before[s].total, known: after[s].known - before[s].known, unassigned: after[s].unassigned - before[s].unassigned });
  expect(d('synthetic-a')).toEqual({ total: 0, known: 0n, unassigned: 0 });
  expect(d('synthetic-manager')).toEqual({ total: 0, known: 0n, unassigned: 0 });
  expect(d('synthetic-manager-other')).toEqual({ total: 1, known: 765432107n, unassigned: 0 });
  // Director scope = all assigned projects: the unassigned draft is not there.
  expect(d('synthetic-director')).toEqual({ total: 1, known: 765432107n, unassigned: 0 });
  expect(d('synthetic-admin')).toEqual({ total: 2, known: 765432107n + 111111n, unassigned: 1 });
  // Raw responses and the rendered page of out-of-scope roles never carry the names.
  for (const s of ['synthetic-a', 'synthetic-manager', 'synthetic-director']) {
    const p = pages[s];
    let raw = '';
    p.on('response', async (r) => {
      if (/workspace-summary|stage-summary/.test(r.url())) raw += await r.text().catch(() => '');
    });
    await p.goto('/');
    await ready(p);
    await expect(p.getByTestId('home-known-total')).toHaveText(yuan((await api(p, '/api/v1/stage-summary')).body.amount.knownTotal));
    expect(raw).not.toContain(tag + '待分配');
    if (s !== 'synthetic-director') expect(raw).not.toContain(tag + '丙组');
    await expect(p.locator('main')).not.toContainText(tag + '待分配');
    if (s === 'synthetic-director') await expect(tile(p, '待分配草稿')).toHaveCount(0);
  }
  // Admin sees the unassigned draft tile grow and its recent list names it.
  const admin = pages['synthetic-admin'];
  await admin.goto('/');
  await ready(admin);
  await expect(tile(admin, '待分配草稿').locator('.task-count')).toHaveText(String(after['synthetic-admin'].unassigned));
  await expect(admin.locator('.recent-list')).toContainText(tag + '待分配');
  for (const p of Object.values(pages)) await p.context().close();
  await c.context().close();
});

// Controlled states: the real response is fetched and then altered, so the
// shape is always the server's.
async function alter(p: Page, path: string, fn: (j: Record<string, unknown>) => void, delay = 0) {
  await p.route(`**/api/v1/${path}`, async (route: Route) => {
    const res = await route.fetch();
    const json = await res.json();
    fn(json);
    if (delay) await new Promise((r) => setTimeout(r, delay));
    await route.fulfill({ response: res, json });
  });
}

test('G3-4-A0 状态：空、加载、慢网、局部失败、403、乱序', async ({ browser }, info) => {
  test.skip(info.project.name !== 'desktop', 'once');
  test.setTimeout(120000);
  const p = await session(browser, 'synthetic-a', { width: 1440, height: 900 });
  // Empty scope.
  await alter(p, 'workspace-summary', (j) => {
    j.counts = Object.fromEntries(Object.keys(j.counts as object).map((k) => [k, 0]));
    j.recent = [];
    j.followups = [];
  });
  await alter(p, 'stage-summary', (j) => {
    const zero = { knownTotal: '0.00', knownCount: 0, knownZeroCount: 0, knownImplicitCount: 0, unknownCount: 0, legacyUnconfirmedCount: 0, invalidCount: 0 };
    j.total = 0;
    j.stages = (j.stages as { code: string }[]).map((s) => ({ code: s.code, count: 0, amount: zero }));
    j.amount = zero;
    j.unclassified = 0;
  });
  await p.goto('/');
  await expect(p.getByRole('heading', { name: '还没有报备' })).toBeVisible();
  await expect(p.getByTestId('home-known-total')).toHaveText('¥0.00');
  await expect(action(p, 'dashboard.stage').first()).toBeDisabled();
  await expect(p.locator('.home-columns')).toHaveCount(0);
  await p.screenshot({ path: shot('state-empty-1440'), fullPage: true });
  await p.unrouteAll();
  // Slow network: loading states visible, then content.
  await alter(p, 'workspace-summary', () => undefined, 1500);
  await alter(p, 'stage-summary', () => undefined, 2500);
  await p.goto('/');
  await expect(p.getByRole('status').filter({ hasText: '正在汇总你的任务' })).toBeVisible();
  await expect(p.locator('.task-board')).toBeVisible();
  await expect(p.getByRole('status').filter({ hasText: '正在加载阶段分布' })).toBeVisible();
  await p.screenshot({ path: shot('state-loading-1440'), fullPage: true });
  await expect(p.locator('.stage-bars')).toBeVisible();
  await p.unrouteAll();
  // Stage panel failure never hides tasks; its own reload recovers.
  await p.route('**/api/v1/stage-summary', (r) => r.abort('failed'));
  await p.goto('/');
  await expect(p.locator('.task-board')).toBeVisible();
  await expect(p.locator('.home-stages').getByRole('alert')).toContainText('无法加载阶段分布');
  await expect(p.locator('.home-amount').getByRole('alert')).toContainText('暂不可用');
  await expect(p.getByTestId('home-known-total')).toHaveCount(0);
  await p.screenshot({ path: shot('state-stage-error-1440'), fullPage: true });
  await p.unroute('**/api/v1/stage-summary');
  await action(p, 'dashboard.stage-reload').click();
  await expect(p.locator('.stage-bars')).toBeVisible();
  // 403 on the task summary: an explicit reason, no numbers.
  await p.route('**/api/v1/workspace-summary', (r) =>
    r.fulfill({ status: 403, contentType: 'application/json', body: JSON.stringify({ code: 'BUSINESS_PERMISSION_DENIED', message: 'denied' }) })
  );
  await p.goto('/');
  await expect(p.getByRole('alert').filter({ hasText: '无法获取任务汇总' })).toBeVisible();
  await expect(p.locator('.task-board')).toHaveCount(0);
  await expect(p.getByRole('alert').filter({ hasText: '请联系管理员核对账号权限' })).toBeVisible();
  await expect(action(p, 'dashboard.reload')).toHaveCount(0); // reloading cannot fix a permission
  await p.screenshot({ path: shot('state-403-1440'), fullPage: true });
  await p.unrouteAll();
  // Out of order: the second request (older refresh) answers after the third.
  let n = 0;
  await p.route('**/api/v1/workspace-summary', async (route: Route) => {
    const mine = ++n;
    const res = await route.fetch();
    const json = await res.json();
    if (mine > 1) (json.counts as Record<string, number>).drafts = mine === 2 ? 111111 : 222222;
    if (mine === 2) await new Promise((r) => setTimeout(r, 2000));
    await route.fulfill({ response: res, json });
  });
  await p.goto('/');
  await ready(p);
  await action(p, 'dashboard.refresh').click();
  await expect(action(p, 'dashboard.refresh')).toHaveText('刷新中…');
  await expect(p.locator('.task-board')).toBeVisible(); // old numbers stay during refresh
  await action(p, 'dashboard.refresh').click();
  await expect(tile(p, '草稿').locator('.task-count')).toHaveText('222222');
  await p.waitForTimeout(2300);
  expect(n).toBe(3);
  await expect(tile(p, '草稿').locator('.task-count')).toHaveText('222222');
  await p.context().close();
});

test('G3-4-A0 长名称、超大数字与历史隐式已知说明（390 与 1440）', async ({ browser }, info) => {
  test.skip(info.project.name !== 'desktop', 'once');
  for (const vp of [viewports[0], viewports[3]]) {
    const p = await session(browser, 'synthetic-director', vp);
    const long = '合成超长项目名称'.repeat(8);
    await alter(p, 'workspace-summary', (j) => {
      const c = j.counts as Record<string, number>;
      for (const k of Object.keys(c)) c[k] = 9876543;
      c.reviewable = 1234567;
      const r = (j.recent as Record<string, unknown>[])[0];
      if (r) {
        r.projectName = long;
        r.customer = '合成超长客户名称'.repeat(6);
      }
    });
    await alter(p, 'stage-summary', (j) => {
      const a = j.amount as Record<string, unknown>;
      a.knownTotal = '1699999999999999960.00';
      a.knownCount = 18001;
      a.knownImplicitCount = 3;
      a.invalidCount = 2;
      const s0 = (j.stages as { count: number; amount: Record<string, unknown> }[])[0];
      s0.count = 9876543;
      s0.amount = { ...s0.amount, knownTotal: '1699999999999999960.00', knownCount: 18001 };
    });
    await p.goto('/');
    await ready(p);
    await expect(p.getByTestId('home-known-total')).toHaveText('¥1,699,999,999,999,999,960.00');
    await expect(p.locator('.home-amount')).toContainText('其中 3 个来自历史记录');
    await expect(p.locator('.amount-split .warn')).toBeVisible();
    await expect(tile(p, '你可独立审核').locator('.task-count')).toHaveText('1234567');
    // The headline amount stays on one line (no wrap inside a digit group).
    const fig = p.getByTestId('home-known-total');
    expect(await fig.evaluate((el) => el.getBoundingClientRect().height < parseFloat(getComputedStyle(el).fontSize) * 2)).toBe(true);
    expect(await fig.evaluate((el) => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
    expect(await noOverflow(p), `overflow @${vp.name}`).toBe(true);
    await p.screenshot({ path: shot(`state-huge-${vp.name}`), fullPage: true });
    await p.context().close();
  }
});

test('G3-4-A0 清理：示例中仍待审的报备由总监退回', async ({ browser }, info) => {
  test.skip(info.project.name !== 'desktop', 'once');
  const d = await session(browser, 'synthetic-director', viewports[0]);
  const list = (await api(d, '/api/v1/projects?pageSize=100&review=pending&q=' + encodeURIComponent(SEED))).body.items as { id: string }[];
  for (const it of list) {
    const cur = (await api(d, `/api/v1/projects/${it.id}`)).body;
    if (['SUBMITTED', 'UNDER_REVIEW'].includes(cur.intakeStatus))
      expect((await api(d, `/api/v1/projects/${it.id}/return`, 'POST', { version: cur.version, reason: '合成测试数据清理' })).status).toBe(200);
  }
  await d.context().close();
});
