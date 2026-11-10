import {
  test as base,
  expect,
  type Browser,
  type Page,
} from '../../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
// G3-4-A1 E2E isolation. The e2e database is shared by every spec of a run
// (desktop, then mobile-390 on the same database in CI) and, locally, can be
// reused across runs. Three rules keep one spec's data from failing another:
//
// 1. Names carry a run identifier (RUN) so a run never mistakes rows left by an
//    earlier run for its own (uniq()).
// 2. Every filing a test submits is registered with the `cleanup` fixture
//    before it is submitted; the fixture returns it after the test, also when
//    the test failed between "submitted" and its last line. A cleanup that
//    cannot be completed fails with project id, registration number, status
//    and reason — it is never swallowed.
// 3. Review cards are located by paging (findCard), never assumed on page 1.
//
// A run that is killed (no teardown at all) leaves its filings behind; the
// next run's global-setup lists them without touching them (rule 1 and 3 make
// them harmless) and, with BAOBEI_E2E_REQUIRE_CLEAN=1, asks for a rebuilt DB.
// Cleanup only ever touches ids this run registered itself.

// Same value in every worker of one run (set by global-setup); a standalone
// config without global setup still gets a fresh identifier per process.
export const RUN = (process.env.BAOBEI_E2E_RUN ??= Date.now().toString(36).slice(-5) + Math.random().toString(36).slice(2, 5));
// A worker restarted after a failure starts a new sequence; the pid keeps
// names from two workers of the same run apart.
let seq = 0;
export const uniq = (prefix: string) => `${prefix}-${RUN}-${process.pid.toString(36)}${++seq}`;

export const action = (p: Page, id: string) => p.locator(`[data-action-id="${id}"]:visible`);

export async function api(p: Page, path: string, method = 'GET', body?: unknown) {
  return p.evaluate(
    async ({ path, method, body }) => {
      const me = await (await fetch('/api/v1/me')).json();
      const r = await fetch(path, {
        method,
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': me.csrfToken, 'Idempotency-Key': 'iso-' + crypto.randomUUID() },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      const text = await r.text();
      let json: unknown = null;
      try {
        json = text ? JSON.parse(text) : null;
      } catch {
        json = { raw: text.slice(0, 200) };
      }
      return { status: r.status, body: json as Record<string, any> }; // eslint-disable-line @typescript-eslint/no-explicit-any
    },
    { path, method, body }
  );
}

const PENDING = ['SUBMITTED', 'UNDER_REVIEW'];
export type CleanupFailure = { id: string; registrationNo?: string; status?: string; reason: string };

// Returns every pending filing in `ids` through an independent reviewer.
// The director sees all assigned projects and never authors test filings; the
// admin is the fallback when the director is not independent of a filing.
export async function returnPending(browser: Browser, ids: string[], label: string): Promise<CleanupFailure[]> {
  if (!ids.length) return [];
  const failures: CleanupFailure[] = [];
  const reviewers: Page[] = [];
  const reviewer = async (i: number) => {
    if (!reviewers[i]) {
      const ctx = await browser.newContext({ baseURL: process.env.BAOBEI_BASE_URL || 'http://127.0.0.1:5188' });
      const p = await ctx.newPage();
      await p.goto('/login');
      await p.locator('select').selectOption(i === 0 ? 'synthetic-director' : 'synthetic-admin');
      await action(p, 'auth.dev-login').click();
      await expect(p.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
      reviewers[i] = p;
    }
    return reviewers[i];
  };
  try {
    for (const id of ids) {
      let last: CleanupFailure | null = null;
      for (const i of [0, 1]) {
        const p = await reviewer(i);
        const cur = await api(p, `/api/v1/projects/${id}`);
        if (cur.status !== 200) {
          last = { id, reason: `GET ${cur.status} ${cur.body?.code ?? ''}`.trim() };
          continue;
        }
        if (!PENDING.includes(cur.body.intakeStatus)) {
          last = null;
          break;
        }
        const r = await api(p, `/api/v1/projects/${id}/return`, 'POST', { version: cur.body.version, reason: '合成测试数据清理' });
        if (r.status === 200) {
          last = null;
          break;
        }
        last = { id, registrationNo: cur.body.registrationNo, status: cur.body.intakeStatus, reason: `return ${r.status} ${r.body?.code ?? ''}`.trim() };
      }
      if (last) failures.push(last);
    }
  } finally {
    for (const p of reviewers) await p?.context().close();
  }
  if (failures.length) console.error(`[e2e-cleanup] ${label}: ${failures.length} filing(s) could not be returned:\n` + failures.map((f) => '  ' + JSON.stringify(f)).join('\n'));
  return failures;
}

// Pending filings of the caller's scope whose name/registration/customer
// matches `tag` (list API `q`). Used for fixtures shared by several tests.
export async function pendingByTag(p: Page, tag: string): Promise<string[]> {
  const ids: string[] = [];
  for (let page = 1; ; page++) {
    const r = await api(p, `/api/v1/projects?review=pending&pageSize=100&page=${page}&q=${encodeURIComponent(tag)}`);
    expect(r.status, 'pending list for cleanup').toBe(200);
    ids.push(...(r.body.items as { id: string }[]).map((x) => x.id));
    if (page * 100 >= r.body.total) return ids;
  }
}

export function assertCleaned(failures: CleanupFailure[], label: string) {
  if (failures.length)
    throw new Error(`E2E cleanup failed (${label}); shared review queue may be polluted:\n` + failures.map((f) => `  project ${f.id} ${f.registrationNo ?? ''} status=${f.status ?? '?'}: ${f.reason}`).join('\n'));
}

export class Cleanup {
  private ids = new Set<string>();
  // Register before submitting, so a failure right after the submit is covered.
  track(id: string) {
    this.ids.add(id);
    return id;
  }
  get tracked() {
    return [...this.ids];
  }
}

export const test = base.extend<{ cleanup: Cleanup }>({
  cleanup: async ({ browser }, use, info) => {
    const c = new Cleanup();
    await use(c);
    // Runs after the test body whether it passed or failed.
    assertCleaned(await returnPending(browser, c.tracked, info.titlePath.join(' › ')), info.title);
  },
});
export { expect };

// Review queue: oldest submission first, 20 per page. In a shared database a
// new filing can be on any page, so page forward one confirmed page at a time
// (the same procedure as g32b-queue since G3-3-P0-R1).
const loadingQueue = (p: Page) => p.getByRole('status').filter({ hasText: '正在加载审核队列' });
export async function findCard(p: Page, text: string) {
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
    throw new Error(`review card not found: target=${text} view=${new URL(p.url()).pathname}${new URL(p.url()).search} pager=${await pager.textContent()}`);
  return card;
}
