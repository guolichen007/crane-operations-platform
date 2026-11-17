import { test, expect, action, api, uniq } from './support/isolation';
import type { Browser, Page } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
import { fileURLToPath } from 'node:url';

// V1.1 R3: project center (one entry, list/board views), six-group board with
// the 12 original stages on demand, 500 cards concentrated in one group,
// column-internal scrolling, old bookmarks, four widths, and the first-login
// name confirmation.
const shot = (name: string) => fileURLToPath(new URL(`../../../local-private/baobei/v11-evidence/${name}.png`, import.meta.url));
async function session(browser: Browser, subject: string, viewport: { width: number; height: number }) {
  const ctx = await browser.newContext({ baseURL: 'http://127.0.0.1:5188', viewport, ...(viewport.width <= 390 ? { isMobile: true, hasTouch: true } : {}) });
  const p = await ctx.newPage();
  await p.goto('/login');
  await p.locator('select').selectOption(subject);
  await action(p, 'auth.dev-login').click();
  await expect(p.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
  return p;
}
// Returns true, or the elements that stick out (diagnosis in the failure).
const noOverflow = (p: Page) =>
  p.evaluate(() => {
    if (document.documentElement.scrollWidth <= innerWidth) return true;
    return Array.from(document.querySelectorAll('body *'))
      .filter((el) => {
        if (el.getBoundingClientRect().right <= innerWidth + 1) return false;
        // Only elements that are not inside a scrolling/clipping container.
        for (let a = el.parentElement; a && a !== document.body; a = a.parentElement)
          if (getComputedStyle(a).overflowX !== 'visible') return false;
        return true;
      })
      .slice(0, 5)
      .map((el) => el.tagName + '.' + (el as HTMLElement).className + ' ' + Math.round(el.getBoundingClientRect().right))
      .join(' | ') + ` sw=${document.documentElement.scrollWidth} iw=${innerWidth} body=${document.body.scrollWidth} wrap=${(document.querySelector('.table-wrap') as HTMLElement | null)?.clientWidth}`;
  });

test.describe.configure({ mode: 'serial' });
let tag = '';
test('V1.1 R3 准备：同一分组集中 500 个项目', async ({ browser }, info) => {
  test.skip(info.project.name !== 'desktop', 'seed once');
  test.setTimeout(150_000);
  tag = uniq('V11密集');
  const p = await session(browser, 'synthetic-c', { width: 1440, height: 900 });
  // 500 drafts in L0/L1 (group G1), created through the real API.
  for (let i = 0; i < 500; i += 25) {
    const batch = [];
    for (let j = i; j < i + 25; j++)
      batch.push(api(p, '/api/v1/projects', 'POST', { projectName: `${tag}-${String(j).padStart(3, '0')}`, customer: `${tag}客户`, salesStage: j % 5 ? 'L0_LEAD' : 'L1_REQUIREMENT', version: 1 }));
    for (const r of await Promise.all(batch)) expect(r.status).toBe(200);
  }
  await api(p, '/api/v1/projects', 'POST', { projectName: `${tag}-赢单`, customer: `${tag}客户`, salesStage: 'WON', version: 1 });
  await p.context().close();
});

for (const vp of [
  { name: '1440', width: 1440, height: 900 },
  { name: '768', width: 768, height: 1024 },
  { name: '390', width: 390, height: 844 },
  { name: '375', width: 375, height: 812 },
]) {
  test(`V1.1 R3 六组管道 @${vp.name}`, async ({ browser }, info) => {
    test.skip(info.project.name !== 'desktop' || !tag, 'viewports set explicitly; needs seed');
    const p = await session(browser, 'synthetic-c', vp);
    const errors: string[] = [];
    p.on('pageerror', (e) => errors.push(e.message));
    // One project-center entry; no separate pipeline entry.
    if (vp.width > 700) {
      await expect(action(p, 'nav.open').filter({ hasText: '项目中心' })).toHaveCount(1);
      await expect(action(p, 'nav.open').filter({ hasText: '项目管道' })).toHaveCount(0);
    }
    await p.goto('/pipeline?q=' + encodeURIComponent(tag));
    const sum = (await api(p, '/api/v1/stage-summary?q=' + encodeURIComponent(tag))).body;
    expect(sum.total).toBe(501);
    const g1 = sum.groups.find((g: { code: string }) => g.code === 'G1').count;
    expect(g1).toBe(500);
    await expect(p.getByRole('heading', { name: /项目中心/ })).toContainText('501');
    const mobile = vp.width <= 700;
    if (mobile) {
      const picker = p.getByRole('combobox', { name: '选择阶段分组' });
      await expect(picker.locator('option')).toHaveCount(6);
      await expect(picker.locator('option[value="G1"]')).toContainText('（500）');
    } else {
      const strip = p.getByRole('navigation', { name: '阶段分布' }).locator('[data-action-id="pipeline.jump"]');
      await expect(strip).toHaveCount(6);
      await expect(strip.filter({ hasText: '商机发现' })).toContainText('500');
      // Empty groups are collapsed but reachable.
      await expect(p.locator('[data-group="G2"] [data-action-id="pipeline.group-open"]')).toBeVisible();
    }
    const col = p.locator('section.stage-column[data-group="G1"]');
    await expect(col.locator('.stage-count')).toHaveText('500');
    await expect(col.locator('.stage-card')).toHaveCount(10);
    await expect(col.locator('.stage-loaded')).toHaveText('已加载 10 / 共 500');
    // Load more twice: 30 cards, no duplicates; the page does not grow with them.
    const height0 = await p.evaluate(() => document.documentElement.scrollHeight);
    for (let i = 0; i < 2; i++) await col.locator('[data-action-id="pipeline.load-more"]').click();
    await expect(col.locator('.stage-card')).toHaveCount(30);
    const names = await col.locator('.stage-card strong').allTextContents();
    expect(new Set(names).size).toBe(30);
    if (!mobile) {
      const height1 = await p.evaluate(() => document.documentElement.scrollHeight);
      expect(height1 - height0).toBeLessThan(80);
      const scrollable = await col.locator('.stage-cards').evaluate((el) => el.scrollHeight > el.clientHeight && getComputedStyle(el).overflowY === 'auto');
      expect(scrollable).toBe(true);
      // The other groups are still reachable while G1 holds 500 cards.
      await p.getByRole('navigation', { name: '阶段分布' }).locator('[data-action-id="pipeline.jump"]').filter({ hasText: '结果归档' }).click();
      await expect(p.locator('section.stage-column[data-group="G6"] .stage-card')).toHaveCount(1);
    }
    // Narrow G1 to one original stage: count equals the 12-stage summary.
    const l1 = sum.stages.find((s: { code: string }) => s.code === 'L1_REQUIREMENT').count;
    await col.locator('[data-action-id="pipeline.sub-stage"]').filter({ hasText: '需求确认' }).click();
    await expect(col.locator('.stage-loaded')).toHaveText(`已加载 10 / 共 ${l1}`);
    expect(await noOverflow(p)).toBe(true);
    await p.evaluate(() => scrollTo(0, 0));
    await p.screenshot({ path: shot(`r3-board-groups-${vp.name}`) });
    // All 12 stages on request.
    await action(p, 'pipeline.layout').filter({ hasText: '12' }).click();
    await expect(p).toHaveURL(/layout=stages/);
    if (mobile) await expect(p.getByRole('combobox', { name: '选择销售阶段' }).locator('option')).toHaveCount(12);
    else await expect(p.getByRole('navigation', { name: '阶段分布' }).locator('[data-action-id="pipeline.jump"]')).toHaveCount(12);
    expect(await noOverflow(p)).toBe(true);
    await p.screenshot({ path: shot(`r3-board-stages-${vp.name}`) });
    // List view with a group filter: the same 500.
    await p.goto('/projects?group=G1&q=' + encodeURIComponent(tag));
    await expect(p.locator('h1 small')).toHaveText('500');
    expect(await noOverflow(p)).toBe(true);
    expect(errors).toEqual([]);
    await p.context().close();
  });
}

test('V1.1 R3 旧书签与返回路径', async ({ browser }, info) => {
  test.skip(info.project.name !== 'desktop' || !tag, 'once; needs seed');
  const p = await session(browser, 'synthetic-c', { width: 1440, height: 900 });
  // An old pipeline bookmark with stage= opens the group narrowed to that stage.
  await p.goto('/pipeline?stage=L1_REQUIREMENT&q=' + encodeURIComponent(tag));
  const col = p.locator('section.stage-column[data-group="G1"]');
  await expect(col.locator('[data-action-id="pipeline.sub-stage"][aria-pressed="true"]')).toContainText('需求确认');
  await col.locator('[data-action-id="project.detail"]').first().click();
  await expect(p).toHaveURL(/\/projects\/[0-9a-f-]+\?from=/);
  await action(p, 'nav.open').filter({ hasText: '返回项目管道' }).click();
  await expect(p).toHaveURL((u) => u.pathname === '/pipeline' && u.searchParams.get('col') === 'L1_REQUIREMENT');
  await expect(col.locator('[data-action-id="pipeline.sub-stage"][aria-pressed="true"]')).toContainText('需求确认');
  // Old list bookmark with a stage keeps working; the project center entry is active.
  await p.goto('/projects?stage=WON&q=' + encodeURIComponent(tag));
  await expect(p.locator('h1 small')).toHaveText('1');
  await p.goto('/pipeline');
  await expect(action(p, 'nav.open').filter({ hasText: '项目中心' })).toHaveAttribute('aria-current', 'page');
  await p.context().close();
});

test('V1.1 R3 首次登录确认姓名：业务前强制、可退出、只改姓名', async ({ page }, info) => {
  test.skip(info.project.name !== 'desktop', 'once');
  await page.goto('/login');
  await page.locator('select').selectOption('synthetic-c');
  // The server gate itself (403 PROFILE_INCOMPLETE, logout allowed, audit) is
  // proved in the Go suite; here /me reports an unconfirmed name to drive the
  // browser redirect, then the real PATCH confirms it.
  await action(page, 'auth.dev-login').click();
  await expect(page.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
  await page.route('**/api/v1/me', async (route) => {
    const r = await route.fetch();
    const body = await r.json();
    await route.fulfill({ response: r, json: { ...body, profileCompleted: false } });
  });
  await page.route('**/api/v1/me/profile', async (route) => {
    if (route.request().method() !== 'GET') return route.continue();
    const r = await route.fetch();
    await route.fulfill({ response: r, json: { ...(await r.json()), completed: false } });
  });
  await page.goto('/projects');
  await expect(page).toHaveURL(/\/profile\?first=1/);
  await expect(page.getByRole('heading', { name: '请确认你的姓名' })).toBeVisible();
  await expect(action(page, 'auth.logout')).toBeVisible();
  await expect(action(page, 'project.create')).toHaveCount(0);
  await page.unroute('**/api/v1/me');
  await page.unroute('**/api/v1/me/profile');
  const name = uniq('示例销售丙');
  await page.locator('[name=displayName]').fill('');
  await action(page, 'profile.save').click();
  await expect(page.locator('#name-error')).toContainText('请填写你的姓名');
  await page.locator('[name=displayName]').fill(name);
  await action(page, 'profile.save').click();
  await expect(page).toHaveURL(/\/projects/);
  await expect(page.locator('.user-name')).toContainText(name);
  await expect(page.locator('.user-name small')).toHaveText('销售');
  const me = (await api(page, '/api/v1/me')).body;
  expect(me).toMatchObject({ name, role: 'baobei_sales', subject: 'synthetic-c' });
  const prof = (await api(page, '/api/v1/me/profile')).body;
  expect(prof.history[0]).toMatchObject({ newName: name, action: 'profile.rename' });
  // Role or team cannot be sent.
  const forged = await api(page, '/api/v1/me/profile', 'PATCH', { name, reason: '', version: prof.version, role: 'baobei_admin' });
  expect(forged.status).toBe(400);
  // Restore the fixture name for other specs.
  const restore = await api(page, '/api/v1/me/profile', 'PATCH', { name: '示例销售 C', reason: '测试复原', version: prof.version });
  expect(restore.status).toBe(200);
});
