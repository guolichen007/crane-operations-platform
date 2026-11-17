// R8 资源与排期最小闭环（真实浏览器 + 有状态模拟 API，规则与服务端 TestR8ScheduleWorkflowHTTP 一致）：
// 创建资源 → 经理配置日历（版本） → 新建排期 → 预约资源 → 批准；第二个排期重叠 → 409 冲突反馈 →
// 冲突页：可覆盖的经理覆盖、硬约束不可覆盖 → 撤回硬资源预约 → 批准 → 已批准排期（基线） → 经理取消（If-Match + 原因，基线保留）。
// 规划员：无批准、日历只读、无覆盖；重复点击批准只发一次请求。
import { test, expect, type Page, type Route } from '@playwright/test';

type Role = 'portfolio_manager' | 'portfolio_planner';
const session = { userId: 'u', displayName: 'u', projects: [], csrfToken: 'x'.repeat(32), expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' };

type Res = { id: string; portfolioScopeId: string; resourceType: string; referenceId: null; name: string; capacity: number; exclusive: boolean; overrideAllowed: boolean; homeTimezone: string; status: string; version: number };
type Item = { id: string; portfolioScopeId: string; projectId: null; sourceType: string; sourceId: string; name: string; startAt: string; endAt: string; timezone: string; status: string; priority: string; exclusiveLocationId: null; version: number };
type Rsv = { id: string; scheduleItemId: string; resourceId: string; startAt: string; endAt: string; quantity: number; status: string; version: number };
type Conf = { id: number; conflictType: string; severity: string; leftItemId: string; rightItemId: string | null; resourceId: string | null; status: string };

async function mock(page: Page, role: Role) {
  const s = {
    role,
    writes: [] as string[],
    approveDelay: 0,
    resources: [] as Res[],
    calendars: new Map<string, { version: number; rules: unknown[] }>(),
    items: [] as Item[],
    rsv: [] as Rsv[],
    conflicts: [] as Conf[],
    baselines: [] as { id: string; portfolioScopeId: string; name: string; baselineVersion: number; approvedBy: string; approvedAt: string }[],
    seq: 0,
  };
  const id = (p: string) => `${p}-${++s.seq}`;
  const overlap = (a: { startAt: string; endAt: string }, b: { startAt: string; endAt: string }) => a.startAt < b.endAt && b.startAt < a.endAt;
  await page.route('**/auth/session', (r) => r.fulfill({ json: session }));
  await page.route('**/api/v1/**', async (r) => {
    const url = new URL(r.request().url());
    const p = url.pathname.replace('/api/v1/', '');
    const m = r.request().method();
    const body = m === 'GET' ? {} : (r.request().postDataJSON() ?? {});
    const manager = s.role === 'portfolio_manager';
    if (m !== 'GET') s.writes.push(`${m} ${p}`);
    if (p === 'portfolio/scopes') return r.fulfill({ json: [{ id: 'PS-A', tenantId: 'demo', code: 'PS-A', name: '甲项目组合', status: 'ACTIVE', version: 1, myRole: s.role }] });
    if (p === 'portfolio/resources' && m === 'GET') return r.fulfill({ json: s.resources });
    if (p === 'portfolio/resources' && m === 'POST') {
      if (!String(body.name ?? '').trim()) return r.fulfill({ status: 422, json: { error: 'name required' } });
      const res: Res = { ...body, id: id('R'), status: 'ACTIVE', version: 1, homeTimezone: 'Asia/Shanghai' };
      s.resources.push(res);
      return r.fulfill({ status: 201, json: res });
    }
    let mm = p.match(/^portfolio\/resources\/([^/]+)\/calendar$/);
    if (mm) {
      const cur = s.calendars.get(mm[1]) ?? { version: 0, rules: [] };
      if (m === 'GET') return r.fulfill({ json: { id: `CAL-${mm[1]}`, timezone: 'Asia/Shanghai', ...cur } });
      if (!manager) return r.fulfill({ status: 403, json: { error: 'Forbidden' } });
      if (body.version !== cur.version) return r.fulfill({ status: 409, json: { error: 'version conflict', code: 'version_conflict' } });
      const next = { version: cur.version + 1, rules: body.rules };
      s.calendars.set(mm[1], next);
      return r.fulfill({ json: { id: `CAL-${mm[1]}`, timezone: 'Asia/Shanghai', ...next } });
    }
    if (p === 'portfolio/schedule/items' && m === 'GET') return r.fulfill({ json: s.items });
    if (p === 'portfolio/schedule/items' && m === 'POST') {
      if (!(body.startAt < body.endAt)) return r.fulfill({ status: 422, json: { error: 'startAt must be before endAt' } });
      const it: Item = { ...body, id: id('SI'), status: 'PLANNED', version: 1 };
      s.items.push(it);
      return r.fulfill({ status: 201, json: it });
    }
    mm = p.match(/^portfolio\/schedule\/items\/([^/]+)\/reservations$/);
    if (mm) {
      if (m === 'GET') return r.fulfill({ json: s.rsv.filter((x) => x.scheduleItemId === mm![1]) });
      const it = s.items.find((x) => x.id === mm![1])!;
      if (body.startAt < it.startAt || body.endAt > it.endAt) return r.fulfill({ status: 422, json: { error: 'reservation outside the schedule item' } });
      const x: Rsv = { ...body, id: id('RV'), scheduleItemId: it.id, status: 'PENDING', version: 1 };
      s.rsv.push(x);
      return r.fulfill({ status: 201, json: x });
    }
    mm = p.match(/^portfolio\/reservations\/([^/]+)\/cancel$/);
    if (mm) {
      const x = s.rsv.find((v) => v.id === mm![1])!;
      if (ifMatch(r) !== x.version) return r.fulfill({ status: 409, json: { error: 'version conflict', code: 'version_conflict' } });
      x.status = 'CANCELLED'; x.version++;
      return r.fulfill({ json: x });
    }
    if (p === 'portfolio/schedule/approve') {
      if (!manager) return r.fulfill({ status: 403, json: { error: 'Forbidden' } });
      if (s.approveDelay) await new Promise((d) => setTimeout(d, s.approveDelay));
      const ids: string[] = body.itemIds;
      const found: Conf[] = [];
      for (const iid of ids) {
        const it = s.items.find((x) => x.id === iid)!;
        if (it.status !== 'PLANNED') return r.fulfill({ status: 409, json: { error: 'item is not PLANNED' } });
        for (const mine of s.rsv.filter((x) => x.scheduleItemId === iid && x.status === 'PENDING')) {
          for (const other of s.rsv.filter((x) => x.status === 'ACTIVE' && x.resourceId === mine.resourceId && overlap(x, mine))) {
            const existing = s.conflicts.find((c) => c.leftItemId === iid && c.resourceId === mine.resourceId);
            if (existing?.status === 'OVERRIDDEN') continue;
            const c = existing ?? { id: ++s.seq, conflictType: 'RESOURCE_OVERLAP', severity: 'BLOCKING', leftItemId: iid, rightItemId: other.scheduleItemId, resourceId: mine.resourceId, status: 'OPEN' };
            if (!existing) s.conflicts.push(c);
            found.push(c);
          }
        }
      }
      if (found.length) return r.fulfill({ status: 409, json: { error: 'blocking schedule conflicts', code: 'schedule_conflict', conflicts: found } });
      for (const iid of ids) {
        const it = s.items.find((x) => x.id === iid)!;
        it.status = 'APPROVED'; it.version++;
        s.rsv.filter((x) => x.scheduleItemId === iid && x.status === 'PENDING').forEach((x) => { x.status = 'ACTIVE'; x.version++; });
        s.conflicts.filter((c) => c.leftItemId === iid && c.status === 'OPEN').forEach((c) => (c.status = 'RESOLVED'));
      }
      s.baselines.unshift({ id: id('BL'), portfolioScopeId: 'PS-A', name: `甲项目组合 排期基线 v${s.baselines.length + 1}`, baselineVersion: s.baselines.length + 1, approvedBy: 'user-pm', approvedAt: '2027-03-01T00:00:00Z' });
      return r.fulfill({ json: { status: 'approved' } });
    }
    mm = p.match(/^portfolio\/schedule\/items\/([^/]+)\/cancel$/);
    if (mm) {
      const it = s.items.find((x) => x.id === mm![1])!;
      if (it.status === 'APPROVED' && !manager) return r.fulfill({ status: 403, json: { error: 'Forbidden' } });
      if (ifMatch(r) !== it.version) return r.fulfill({ status: 409, json: { error: 'version conflict', code: 'version_conflict' } });
      it.status = 'CANCELLED'; it.version++;
      s.rsv.filter((x) => x.scheduleItemId === it.id).forEach((x) => (x.status = 'CANCELLED'));
      return r.fulfill({ json: it });
    }
    if (p === 'portfolio/conflicts') return r.fulfill({ json: s.conflicts.filter((c) => c.status === 'OPEN') });
    mm = p.match(/^portfolio\/conflicts\/(\d+)\/override$/);
    if (mm) {
      if (!manager) return r.fulfill({ status: 403, json: { error: 'Forbidden' } });
      const c = s.conflicts.find((x) => x.id === Number(mm![1]))!;
      if (s.resources.find((x) => x.id === c.resourceId)?.overrideAllowed === false) return r.fulfill({ status: 409, json: { error: 'hard conflict cannot be overridden' } });
      if (String(body.reason ?? '').trim().length < 8) return r.fulfill({ status: 422, json: { error: 'reason must be at least 8 characters' } });
      c.status = 'OVERRIDDEN';
      return r.fulfill({ json: { status: 'overridden' } });
    }
    if (p === 'portfolio/baselines') return r.fulfill({ json: s.baselines });
    return r.fulfill({ json: [] });
  });
  return s;
}

const ifMatch = (r: Route) => Number((r.request().headers()['if-match'] ?? '').replace(/"/g, ''));
const tab = (page: Page, name: string) => page.locator('.tabs').getByRole('button', { name, exact: true });
async function createResource(page: Page, name: string, overrideAllowed: boolean) {
  await page.getByTestId('resource-create').click();
  const f = page.getByTestId('resource-form');
  await f.getByLabel('资源名称').fill(name);
  if (!overrideAllowed) await f.getByLabel('冲突可覆盖').uncheck();
  await f.getByTestId('resource-submit').click();
  await expect(page.getByTestId('resource-table')).toContainText(name);
}
async function createItem(page: Page, name: string, start: string, end: string) {
  await page.getByTestId('schedule-create').click();
  const f = page.getByTestId('schedule-form');
  await f.getByLabel('排期名称').fill(name);
  await f.getByLabel('排期开始').fill(start);
  await f.getByLabel('排期结束').fill(end);
  await f.getByTestId('schedule-submit').click();
  await expect(page.getByTestId('schedule-table')).toContainText(name);
}
async function reserve(page: Page, item: string, resource: RegExp) {
  await page.locator('tr', { hasText: item }).getByTestId('schedule-open').click();
  await page.getByTestId('reservation-create').click();
  const f = page.getByTestId('reservation-form');
  await f.getByLabel('预约资源').selectOption({ label: (await f.getByLabel('预约资源').locator('option', { hasText: resource }).textContent())! });
  await f.getByTestId('reservation-submit').click();
  await expect(page.getByTestId('reservation-table')).toContainText(resource);
}

test('经理：资源 → 日历 → 排期 → 预约 → 批准 → 冲突反馈与处理 → 已批准历史 → 取消', async ({ page }) => {
  const s = await mock(page, 'portfolio_manager');
  await page.goto('/#/portfolio');
  await tab(page, '资源').click();
  await expect(page.getByTestId('resource-empty')).toBeVisible();
  await createResource(page, '调试一班', true);
  await createResource(page, '三号轨道窗口', false);
  await expect(page.locator('tr', { hasText: '三号轨道窗口' })).toContainText('不可覆盖');

  // Calendar: manager edits with version; a stale version is reported, input kept.
  await page.locator('tr', { hasText: '调试一班' }).getByTestId('resource-calendar').click();
  await page.getByTestId('calendar-add').click();
  await page.getByTestId('calendar-submit').click();
  await expect(page.getByTestId('schedule-notice')).toContainText('日历已保存（v1）');
  s.calendars.set('R-1', { version: 5, rules: [] });
  await page.locator('tr', { hasText: '调试一班' }).getByTestId('resource-calendar').click();
  s.calendars.set('R-1', { version: 6, rules: [] }); // changed by someone else after opening
  await page.getByTestId('calendar-submit').click();
  await expect(page.getByTestId('schedule-action-error')).toContainText('已被他人修改');
  await expect(page.getByTestId('calendar-form')).toBeVisible();
  await page.getByTestId('calendar-form').getByRole('button', { name: '关闭' }).click();

  await tab(page, '排期').click();
  await createItem(page, '一号行车调试', '2027-03-01T09:00', '2027-03-01T13:00');
  await createItem(page, '二号行车调试', '2027-03-01T11:00', '2027-03-01T15:00');
  await reserve(page, '一号行车调试', /调试一班/);
  await reserve(page, '一号行车调试', /三号轨道窗口/);
  await reserve(page, '二号行车调试', /调试一班/);
  await reserve(page, '二号行车调试', /三号轨道窗口/);

  await page.locator('tr', { hasText: '一号行车调试' }).getByTestId('schedule-approve').click();
  await expect(page.getByTestId('schedule-notice')).toContainText('排期已批准');
  await expect(page.locator('tr', { hasText: '一号行车调试' })).toContainText('已批准');

  // Overlap: 409 schedule_conflict is explained; nothing approved.
  await page.locator('tr', { hasText: '二号行车调试' }).getByTestId('schedule-approve').click();
  await expect(page.getByTestId('schedule-action-error')).toContainText('阻断冲突');
  await expect(page.locator('tr', { hasText: '二号行车调试' })).toContainText('计划中');

  await tab(page, '冲突').click();
  await expect(page.getByTestId('conflict-table')).toContainText('独占资源时段重叠');
  await expect(page.locator('tr', { hasText: '三号轨道窗口' }).getByTestId('override-hard')).toBeVisible();
  page.once('dialog', (d) => void d.accept('现场确认两台调试可以共用班组'));
  await page.locator('tr', { hasText: '调试一班' }).getByTestId('conflict-override').click();
  await expect(page.getByTestId('schedule-notice')).toContainText('冲突已覆盖');
  await expect(page.locator('tr', { hasText: '调试一班' })).toHaveCount(0);

  // Hard conflict: withdraw the runway reservation, then approve.
  await tab(page, '排期').click();
  await page.locator('tr', { hasText: '二号行车调试' }).getByTestId('schedule-open').click();
  await page.getByTestId('reservation-table').locator('tr', { hasText: '三号轨道窗口' }).getByTestId('reservation-cancel').click();
  await expect(page.getByTestId('reservation-table').locator('tr', { hasText: '三号轨道窗口' })).toContainText('已取消');
  await page.locator('tr', { hasText: '二号行车调试' }).getByTestId('schedule-approve').click();
  await expect(page.locator('tr', { hasText: '二号行车调试' })).toContainText('已批准');

  await tab(page, '排期历史').click();
  await expect(page.getByTestId('baseline-table').locator('tbody tr')).toHaveCount(2);
  await expect(page.getByTestId('baseline-table')).toContainText('user-pm');

  await tab(page, '排期').click();
  page.once('dialog', (d) => void d.accept('客户延期至四月'));
  await page.locator('tr', { hasText: '二号行车调试' }).getByTestId('schedule-cancel').click();
  await expect(page.locator('tr', { hasText: '二号行车调试' })).toContainText('已取消');
  await tab(page, '排期历史').click();
  await expect(page.getByTestId('baseline-table').locator('tbody tr')).toHaveCount(2);
  const cancel = s.writes.filter((w) => w.endsWith('/cancel'));
  expect(cancel).toHaveLength(2); // reservation + item
  await page.screenshot({ path: `${process.env.R6_EVIDENCE_DIR ?? 'test-results'}/r8-schedule-history.png` });
});

test('规划员：可建资源/排期/预约；无批准按钮，日历只读，冲突不可覆盖；批准中重复点击只发一次', async ({ page }) => {
  const s = await mock(page, 'portfolio_planner');
  await page.goto('/#/portfolio');
  await tab(page, '资源').click();
  await createResource(page, '调试一班', true);
  await page.locator('tr', { hasText: '调试一班' }).getByTestId('resource-calendar').click();
  await expect(page.getByTestId('calendar-submit')).toHaveCount(0);
  await expect(page.getByTestId('calendar-add')).toHaveCount(0);
  await page.getByTestId('calendar-form').getByRole('button', { name: '关闭' }).click();
  await tab(page, '排期').click();
  await createItem(page, '一号行车调试', '2027-03-01T09:00', '2027-03-01T13:00');
  await reserve(page, '一号行车调试', /调试一班/);
  await expect(page.getByTestId('schedule-approve')).toHaveCount(0);
  await expect(page.getByTestId('schedule-cancel')).toHaveCount(1); // PLANNED: planner may withdraw
  s.conflicts.push({ id: 99, conflictType: 'RESOURCE_OVERLAP', severity: 'BLOCKING', leftItemId: 'SI-2', rightItemId: null, resourceId: 'R-1', status: 'OPEN' });
  await tab(page, '冲突').click();
  await expect(page.getByTestId('override-not-allowed')).toBeVisible();
  await expect(page.getByTestId('conflict-override')).toHaveCount(0);

  // Manager again (role changes server-side): slow approve, double click → exactly one POST.
  s.role = 'portfolio_manager';
  s.approveDelay = 800;
  await page.reload();
  await tab(page, '排期').click();
  const approve = page.locator('tr', { hasText: '一号行车调试' }).getByTestId('schedule-approve');
  await approve.click();
  await approve.click({ force: true }).catch(() => undefined);
  await expect(page.locator('tr', { hasText: '一号行车调试' })).toContainText('已批准');
  expect(s.writes.filter((w) => w.endsWith('/approve'))).toHaveLength(1);
});

test('输入校验与移动端：结束早于开始被服务器拒绝时显示原因且表单保留；375 宽可操作', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await mock(page, 'portfolio_manager');
  await page.goto('/#/portfolio');
  await tab(page, '排期').click();
  await page.getByTestId('schedule-create').click();
  const f = page.getByTestId('schedule-form');
  await f.getByLabel('排期名称').fill('反向时段');
  await f.getByLabel('排期开始').fill('2027-03-01T13:00');
  await f.getByLabel('排期结束').fill('2027-03-01T09:00');
  await f.getByTestId('schedule-submit').click();
  await expect(page.getByTestId('schedule-action-error')).toContainText('输入不符合要求');
  await expect(f.getByLabel('排期名称')).toHaveValue('反向时段');
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(1);
  await page.screenshot({ path: `${process.env.R6_EVIDENCE_DIR ?? 'test-results'}/r8-schedule-375.png` });
});
