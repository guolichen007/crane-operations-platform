// Q1 project registration — real browser against a stateful mocked API. The mock enforces the
// same rules as the server (unique code per scope, state machine, manager-only approval, no
// auto-grant), so these specs exercise the UI behaviour: create → reload → advance → approve,
// 403/409/422/503 messages, failure never shown as "暂无", draft guard and double submit.
// The same flow against the real BFF + PostgreSQL is TestPortfolioIntakeRegistrationFlowHTTP.
import { test, expect, type Page } from '@playwright/test';

type Role = 'portfolio_manager' | 'portfolio_planner' | 'none';
type Intake = Record<string, unknown> & { id: string; intakeCode: string; status: string; formalProjectId: string | null; portfolioScopeId: string; projectName: string };
const scope = { id: 'PS-A', tenantId: 'demo', code: 'PS-A', name: '华东项目组合', status: 'ACTIVE', version: 1 };
const session = { userId: 'u', displayName: 'pm', projects: [], csrfToken: 'x'.repeat(32), expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' };
const shot = (name: string) => `test-results/q1-portfolio/${name}.png`;

async function mockApi(page: Page, opts: { role?: Role; fail?: Set<string>; createDelayMs?: number } = {}) {
  const role = opts.role ?? 'portfolio_manager';
  const fail = opts.fail ?? new Set<string>();
  const intakes: Intake[] = [];
  const posts: string[] = [];
  let seq = 0;
  await page.route('**/auth/session', (r) => r.fulfill({ json: session }));
  await page.route('**/api/v1/**', async (r) => {
    const url = new URL(r.request().url());
    const p = url.pathname.replace('/api/v1/', '');
    const m = r.request().method();
    if (m !== 'GET') posts.push(`${m} ${p}`);
    if (fail.has(`${m} ${p}`)) return r.fulfill({ status: 503, json: { error: 'database unavailable' } });
    if (p === 'portfolio/scopes') return r.fulfill({ json: role === 'none' ? [] : [scope] });
    if (p === 'portfolio/intakes' && m === 'GET') return r.fulfill({ json: [...intakes].reverse() });
    if (p === 'portfolio/intakes' && m === 'POST') {
      if (opts.createDelayMs) await new Promise((x) => setTimeout(x, opts.createDelayMs));
      const b = r.request().postDataJSON() as Record<string, unknown>;
      if (role === 'none') return r.fulfill({ status: 403, json: { error: 'forbidden' } });
      if (!String(b.projectName ?? '').trim()) return r.fulfill({ status: 422, json: { error: 'invalid intake input: projectName is required' } });
      if (intakes.some((i) => i.intakeCode === b.intakeCode)) return r.fulfill({ status: 409, json: { error: 'intake code already exists in this portfolio' } });
      const created: Intake = { ...b, id: `I${++seq}`, tenantId: 'demo', registeredAt: new Date().toISOString(), registeredBy: 'u', status: 'REGISTERED', formalProjectId: null, version: 1 } as unknown as Intake;
      intakes.push(created);
      return r.fulfill({ status: 201, json: created });
    }
    const t = /^portfolio\/intakes\/([^/]+)\/(transitions|approve)$/.exec(p);
    if (t) {
      const it = intakes.find((i) => i.id === t[1]);
      if (!it) return r.fulfill({ status: 404, json: { error: 'not found' } });
      if (t[2] === 'approve') {
        if (role !== 'portfolio_manager') return r.fulfill({ status: 403, json: { error: 'forbidden' } });
        if (it.status !== 'PLANNING') return r.fulfill({ status: 409, json: { error: 'intake is not ready to approve' } });
        it.status = 'APPROVED'; it.formalProjectId = `FP-${it.id}`;
        return r.fulfill({ json: { projectId: it.formalProjectId } });
      }
      const next = (r.request().postDataJSON() as { status: string }).status;
      const ok = ({ REGISTERED: 'REVIEW', REVIEW: 'PLANNING' } as Record<string, string>)[it.status] === next;
      if (!ok) return r.fulfill({ status: 409, json: { error: 'invalid transition' } });
      it.status = next;
      return r.fulfill({ json: { status: 'transitioned' } });
    }
    // Inventory / map projects: the formal project never appears here (no auto-grant, no lifecycle).
    if (p === 'projects') return r.fulfill({ json: [] });
    return r.fulfill({ json: [] });
  });
  return { intakes, posts };
}

async function fillRequired(page: Page, code = 'INT-Q1-001', name = 'Q1 华东新装项目') {
  const form = page.getByTestId('intake-create-form');
  await form.getByLabel('登记编号').fill(code);
  await form.getByLabel('项目类型').fill('NEW_INSTALLATION');
  await form.getByLabel('项目名称').fill(name);
  return form;
}

test('有权限账号：新建登记 → 刷新仍在 → 推进 → 批准返回正式项目 ID，且不自动授权、不进地图', async ({ page }) => {
  const api = await mockApi(page);
  await page.goto('/#/portfolio');
  await expect(page.getByTestId('intake-empty')).toBeVisible();
  await page.getByTestId('intake-create').click();
  const form = await fillRequired(page);
  await form.getByLabel('计划开始').fill('2026-12-01');
  await form.getByLabel('计划结束').fill('2027-03-01');
  await page.screenshot({ path: shot('1-create-form') });
  await form.getByRole('button', { name: '保存登记' }).click();
  await expect(form).toBeHidden();
  await expect(page.getByTestId('portfolio-notice')).toContainText('INT-Q1-001');
  const sent = api.intakes[0];
  expect(sent.priority).toBe('MEDIUM');
  expect(sent.projectManagerUserId).toBeNull();
  expect(sent.plannedEnd).toMatch(/^2027-0[23]-/);
  await page.reload();
  const row = page.locator('[data-intake-id="I1"]');
  await expect(row).toContainText('Q1 华东新装项目');
  await expect(row.getByTestId('intake-stages')).toContainText('尚未生成正式项目');
  await row.getByTestId('intake-advance').click();
  await expect(row).toContainText('评审中');
  await row.getByTestId('intake-advance').click();
  await expect(row).toContainText('规划中');
  page.once('dialog', (d) => { expect(d.message()).toContain('不会自动授予'); void d.accept(); });
  await row.getByTestId('intake-approve').click();
  await expect(page.getByTestId('portfolio-notice')).toContainText('FP-I1');
  await expect(row).toContainText('已批准');
  const stages = row.getByTestId('intake-stages');
  await expect(stages).toContainText('已生成正式项目 FP-I1');
  await expect(stages).toContainText('尚未获得该项目的访问');
  await expect(stages).toContainText('未进入运营地图');
  await expect(row.getByRole('button')).toHaveCount(0);
  await page.screenshot({ path: shot('2-approved-stages') });
});

test('无权限账号看不到新建入口；规划者批准得到 403 明确提示', async ({ page }) => {
  await mockApi(page, { role: 'none' });
  await page.goto('/#/portfolio');
  await expect(page.getByTestId('intake-no-scope')).toBeVisible();
  await expect(page.getByTestId('intake-create')).toHaveCount(0);
  await page.screenshot({ path: shot('3-no-permission') });

  const page2 = await page.context().newPage();
  const api = await mockApi(page2, { role: 'portfolio_planner' });
  api.intakes.push({ id: 'I9', intakeCode: 'X', projectName: '待批项目', status: 'PLANNING', formalProjectId: null, portfolioScopeId: 'PS-A', priority: 'MEDIUM', plannedStart: null, plannedEnd: null, customerName: null });
  await page2.goto('/#/portfolio');
  page2.once('dialog', (d) => void d.accept());
  await page2.locator('[data-intake-id="I9"]').getByTestId('intake-approve').click();
  await expect(page2.getByTestId('portfolio-action-error')).toContainText('无批准权限');
  await expect(page2.locator('[data-intake-id="I9"]')).toContainText('规划中');
});

test('503 显示错误与重试，不显示「暂无」；恢复后列表正常', async ({ page }) => {
  const fail = new Set(['GET portfolio/intakes', 'GET portfolio/resources', 'GET portfolio/schedule/items', 'GET portfolio/conflicts']);
  await mockApi(page, { fail });
  await page.goto('/#/portfolio');
  await expect(page.getByTestId('intake-error')).toContainText('服务暂不可用');
  await expect(page.getByText('暂无项目登记')).toHaveCount(0);
  await page.screenshot({ path: shot('4-503-error') });
  for (const [tab, id] of [['资源', 'resource-error'], ['排期', 'schedule-error'], ['冲突', 'conflict-error']]) {
    await page.getByRole('tab', { name: tab }).or(page.locator('.tabs button', { hasText: tab })).first().click();
    await expect(page.getByTestId(id)).toBeVisible();
  }
  await expect(page.getByText(/暂无资源|暂无排期项|无未解决冲突/)).toHaveCount(0);
  fail.clear();
  await page.locator('.tabs button', { hasText: '项目登记' }).click();
  await page.getByTestId('intake-error').getByRole('button', { name: '重试' }).click();
  await expect(page.getByTestId('intake-empty')).toBeVisible();
});

test('保存失败保留输入：422/409/503 原位提示，重试不清空', async ({ page }) => {
  const fail = new Set<string>();
  const api = await mockApi(page, { fail });
  api.intakes.push({ id: 'I0', intakeCode: 'DUP', projectName: '已有', status: 'REGISTERED', formalProjectId: null, portfolioScopeId: 'PS-A', priority: 'MEDIUM', plannedStart: null, plannedEnd: null, customerName: null });
  await page.goto('/#/portfolio');
  await page.getByTestId('intake-create').click();
  const form = page.getByTestId('intake-create-form');
  await form.getByRole('button', { name: '保存登记' }).click();
  await expect(form.locator('.field-error')).toHaveCount(3);
  expect(api.posts).toHaveLength(0);
  await fillRequired(page, 'DUP');
  await form.getByRole('button', { name: '保存登记' }).click();
  await expect(page.getByTestId('intake-form-error')).toContainText('已存在相同登记编号');
  fail.add('POST portfolio/intakes');
  await form.getByLabel('登记编号').fill('INT-NEW');
  await form.getByRole('button', { name: '保存登记' }).click();
  await expect(page.getByTestId('intake-form-error')).toContainText('已保留');
  await expect(form.getByLabel('项目名称')).toHaveValue('Q1 华东新装项目');
  await expect(form.getByLabel('登记编号')).toHaveValue('INT-NEW');
  fail.clear();
  await form.getByRole('button', { name: '保存登记' }).click();
  await expect(form).toBeHidden();
  await expect(page.locator('[data-intake-id="I1"]')).toContainText('INT-NEW');
});

test('草稿保护与防重复提交：取消/Esc/切页需确认，保存中不可关闭且只发一次', async ({ page }) => {
  const api = await mockApi(page, { createDelayMs: 1200 });
  await page.goto('/#/portfolio');
  await page.getByTestId('intake-create').click();
  const form = await fillRequired(page);
  const prompts: string[] = [];
  page.on('dialog', (d) => { prompts.push(d.message()); void d.dismiss(); });
  await form.getByRole('button', { name: '取消' }).click();
  await page.keyboard.press('Escape');
  await page.evaluate(() => (location.hash = '#/assets'));
  await expect(form).toBeVisible();
  await expect(page).toHaveURL(/#\/portfolio/);
  expect(prompts.length).toBe(3);
  await expect(form.getByLabel('项目名称')).toHaveValue('Q1 华东新装项目');
  const save = form.getByRole('button', { name: '保存登记' });
  await save.click();
  await expect(form.getByRole('button', { name: '保存中…' })).toBeDisabled();
  await form.getByRole('button', { name: '保存中…' }).click({ force: true }).catch(() => {});
  await page.keyboard.press('Enter');
  await page.keyboard.press('Escape');
  await expect(form).toBeVisible();
  await expect(form).toBeHidden({ timeout: 5000 });
  expect(api.posts.filter((p) => p === 'POST portfolio/intakes')).toHaveLength(1);
});

for (const [w, h] of [[1280, 720], [1366, 768], [1920, 1080]] as const) {
  test(`表单与操作按钮在 ${w}×${h} 可用`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: h });
    const api = await mockApi(page);
    api.intakes.push({ id: 'I5', intakeCode: 'INT-5', projectName: '待推进', status: 'PLANNING', formalProjectId: null, portfolioScopeId: 'PS-A', priority: 'HIGH', plannedStart: null, plannedEnd: null, customerName: '客户A' });
    await page.goto('/#/portfolio');
    const approve = page.locator('[data-intake-id="I5"]').getByTestId('intake-approve');
    await approve.scrollIntoViewIfNeeded();
    await expect(approve).toBeInViewport();
    await page.getByTestId('intake-create').click();
    const form = page.getByTestId('intake-create-form');
    await form.getByRole('button', { name: '更多信息（可选）' }).click();
    for (const name of ['保存登记', '取消']) {
      const b = form.getByRole('button', { name });
      await b.scrollIntoViewIfNeeded();
      await expect(b).toBeInViewport();
      await expect(b).toBeEnabled();
    }
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    await page.screenshot({ path: shot(`5-form-${w}x${h}`) });
  });
}
