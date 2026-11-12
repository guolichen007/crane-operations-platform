// Q4-QA-R1a §3: the location form must never create project A's location in project B. Same admin
// holds location_write on BOTH projects; the top bar switches ?project= while the page (same
// component) stays mounted. Real browser, stateful mock that records the project of each POST.
import { test, expect, type Page } from '@playwright/test';

const session = { userId: 'u', displayName: 'admin', projects: [{ projectId: 'PA', projectName: '项目甲', role: 'platform_admin' }, { projectId: 'PB', projectName: '项目乙', role: 'platform_admin' }], csrfToken: 'x'.repeat(32), expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' };
type Loc = { id: string; projectId: string; parentId: string | null; kind: string; name: string; status: string; code: string | null };
const full = (l: Loc) => ({ province: '', city: '', county: '', longitude: null, latitude: null, coordinateSystem: 'UNKNOWN', coordinateSource: 'MANUAL', version: 1, address: '', ...l });

async function mock(page: Page, opts: { hold?: boolean } = {}) {
  const state = {
    locations: [{ id: 'A-SITE', projectId: 'PA', parentId: null, kind: 'SITE', name: '甲场地', status: 'ACTIVE', code: 'AS' }, { id: 'B-SITE', projectId: 'PB', parentId: null, kind: 'SITE', name: '乙场地', status: 'ACTIVE', code: 'BS' }] as Loc[],
    posts: [] as { project: string; body: Loc }[],
    release: () => {},
  };
  let gate: Promise<void> | null = opts.hold ? new Promise<void>((r) => (state.release = r)) : null;
  await page.route('**/auth/session', (r) => r.fulfill({ json: session }));
  await page.route('**/api/v1/**', async (r) => {
    const m = /^\/api\/v1\/projects\/(P[AB])\/locations$/.exec(new URL(r.request().url()).pathname);
    if (m && r.request().method() === 'GET') return r.fulfill({ json: state.locations.filter((l) => l.projectId === m[1]).map(full) });
    if (m && r.request().method() === 'POST') {
      const body = r.request().postDataJSON() as Loc;
      state.posts.push({ project: m[1], body });
      if (gate) { await gate; gate = null; }
      // Server rule: the URL path is the project; a parent must be in that project.
      if (body.parentId && !state.locations.some((l) => l.id === body.parentId && l.projectId === m[1])) return r.fulfill({ status: 422, json: { error: 'invalid input: parent location not found in this project' } });
      const loc = { ...body, id: `N${state.posts.length}`, projectId: m[1], status: 'ACTIVE' };
      state.locations.push(loc);
      return r.fulfill({ status: 201, json: full(loc) });
    }
    return r.fulfill({ json: [] });
  });
  return state;
}
async function fillA(page: Page) {
  await page.goto('/#/locations?project=PA');
  await expect(page.getByTestId('location-table')).toContainText('甲场地');
  await page.getByTestId('location-create').click();
  const form = page.getByTestId('location-create-form');
  await form.getByLabel('位置类型').selectOption('WORKSHOP');
  await form.getByLabel('位置名称').fill('甲一车间');
  await form.getByLabel('上级位置').selectOption({ label: '甲场地（场地）' });
  return form;
}
/** The top bar's project selector (same page component, only ?project= changes). */
async function switchToB(page: Page) {
  await page.evaluate(() => (location.hash = '#/locations?project=PB'));
}

test('甲项目的位置草稿：切换到乙项目需确认；拒绝则留在甲，确认放弃后乙无表单，乙从未被写入', async ({ page }) => {
  const state = await mock(page);
  const form = await fillA(page);
  let accept = false;
  const prompts: string[] = [];
  page.on('dialog', async (d) => { prompts.push(d.message()); accept ? await d.accept() : await d.dismiss(); });
  await switchToB(page);
  await expect(page).toHaveURL(/project=PA/);
  await expect(form.getByLabel('位置名称')).toHaveValue('甲一车间');
  expect(prompts.length).toBeGreaterThan(0);
  accept = true;
  await switchToB(page);
  await expect(page).toHaveURL(/project=PB/);
  await expect(page.getByTestId('location-create-form')).toHaveCount(0);
  await expect(page.getByTestId('location-table')).toContainText('乙场地');
  expect(state.posts).toEqual([]);
});

test('切换项目后新建：表单标题、上级候选与写入项目都是乙，不含甲的位置', async ({ page }) => {
  const state = await mock(page);
  page.on('dialog', (d) => void d.accept());
  await fillA(page);
  await switchToB(page);
  await expect(page.getByTestId('location-table')).toContainText('乙场地');
  await page.getByTestId('location-create').click();
  const form = page.getByTestId('location-create-form');
  await expect(form).toContainText('PB');
  await expect(form.getByLabel('上级位置').locator('option', { hasText: '甲场地' })).toHaveCount(0);
  await form.getByLabel('位置类型').selectOption('WORKSHOP');
  await form.getByLabel('位置名称').fill('乙一车间');
  await form.getByLabel('上级位置').selectOption({ label: '乙场地（场地）' });
  await form.getByTestId('location-create-submit').click();
  await expect(form).toBeHidden();
  expect(state.posts.map((p) => [p.project, p.body.name, p.body.parentId])).toEqual([['PB', '乙一车间', 'B-SITE']]);
});

test('提交中切换项目：被拒绝；请求完成后写入的是打开表单时的甲项目', async ({ page }) => {
  const state = await mock(page, { hold: true });
  let prompts = 0;
  page.on('dialog', async (d) => { prompts++; await d.accept(); });
  const form = await fillA(page);
  await form.getByTestId('location-create-submit').click();
  await expect(form.getByTestId('location-create-submit')).toBeDisabled();
  await switchToB(page);
  await expect(page).toHaveURL(/project=PA/);
  expect(prompts).toBe(0);
  state.release();
  await expect(form).toBeHidden();
  expect(state.posts.map((p) => p.project)).toEqual(['PA']);
});
