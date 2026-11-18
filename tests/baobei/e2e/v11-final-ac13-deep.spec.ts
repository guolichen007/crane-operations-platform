import { test, expect, action, api, uniq } from './support/isolation';
import type { Page, Locator } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';

// V1.1 final (UAT AC-13): deep scroll (~835px) + preview panel. The position
// survives opening/closing the preview, opening the detail from the preview and
// returning by the in-app button or browser back; the same project is still on
// screen afterwards (not only a number within tolerance). Desktop and 390.
async function login(p: Page, subject: string) {
  await p.goto('/login');
  await p.locator('select').selectOption(subject);
  await action(p, 'auth.dev-login').click();
  await expect(p.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
}
const scrollY = (p: Page) => p.evaluate(() => Math.round(window.scrollY));
async function onScreen(p: Page, el: Locator) {
  const c = await el.count();
  if (c !== 1) return 'count=' + c + ' rows=' + (await p.locator('.table-wrap tbody tr, .mobile-projects [data-action-id="project.preview"]').count()) + ' url=' + p.url() + ' names=' + (await p.locator('td.project-cell, .mobile-projects strong').allTextContents()).slice(0, 3).join(',');
  const box = await el.boundingBox({ timeout: 2000 }).catch(() => null);
  const vh = await p.evaluate(() => window.innerHeight);
  return !!box && box.y >= 0 && box.y + Math.min(box.height, 40) <= vh ? true : JSON.stringify({ box, vh, y: await p.evaluate(() => scrollY) });
}

for (const latency of [0, 800])
test(`V1.1 AC-13 深滚动预览、关闭、详情、返回与后退保持位置和原项目可见（接口延迟 ${latency}ms）`, async ({ page }, info) => {
  test.setTimeout(latency ? 150000 : 60000);
  await login(page, 'synthetic-b');
  const tag = uniq('AC13深');
  for (let i = 0; i < 40; i += 10) {
    const batch = [];
    for (let j = i; j < i + 10; j++)
      batch.push(api(page, '/api/v1/projects', 'POST', { projectName: `${tag}-${String(j).padStart(2, '0')}`, customer: tag + '客户', salesStage: 'L2_TECHNICAL', version: 1 }));
    for (const r of await Promise.all(batch)) expect(r.status).toBe(200);
  }
  // UAT: a slow network (the list reload after "next page" finishes while the
  // user has already scrolled deep and opened a preview).
  if (latency) await page.route(/\/api\/v1\//, async (r) => { await new Promise((ok) => setTimeout(ok, latency)); await r.continue(); });
  await page.goto(`/projects?q=${encodeURIComponent(tag)}&sort=name`);
  await expect(page.locator('h1 small')).toHaveText('40');
  await action(page, 'project.page-next').click();
  await expect(page).toHaveURL(/page=2/);
  await expect(page.locator('.pagination')).toContainText('第 2 页');
  await expect(page.locator(info.project.name === 'mobile-390' ? '.mobile-projects strong' : 'td.project-cell').first()).toHaveText(tag + '-20');
  const mobile = info.project.name === 'mobile-390';
  const rows = mobile ? page.locator('.mobile-projects [data-action-id="project.preview"]') : page.locator('.table-wrap tbody tr');
  await expect(rows).toHaveCount(20);
  // Deep scroll like the server report (~835px); pick the row nearest the middle.
  await page.evaluate(() => window.scrollTo(0, 835));
  // 835px, or the bottom of the page when page 2 is shorter (desktop).
  await expect.poll(() => scrollY(page)).toBeGreaterThan(mobile ? 700 : 600);
  const before = await scrollY(page);
  const mid = await rows.evaluateAll((els) => {
    const c = window.innerHeight / 2;
    let best = 0, d = 1e9;
    els.forEach((e, i) => { const r = e.getBoundingClientRect(); const dd = Math.abs(r.top + r.height / 2 - c); if (dd < d) { d = dd; best = i; } });
    return best;
  });
  const target = rows.nth(mid);
  const name = (await target.textContent())!.match(new RegExp(tag + '-\\d\\d'))![0];
  // The exact project (not "-3" matching "-30"…): by its own name element.
  const same = mobile
    ? page.locator('.mobile-projects [data-action-id="project.preview"]').filter({ has: page.locator('strong').getByText(name, { exact: true }) })
    : page.locator('.table-wrap tbody tr').filter({ has: page.locator('td.project-cell').getByText(name, { exact: true }) });
  // 1. Open and close the preview (close button, then Esc): position and project stay.
  for (const close of ['button', 'esc'] as const) {
    if (mobile) await target.click(); else await target.locator('td.project-cell').click();
    await expect(page.getByRole('dialog')).toBeVisible();
    expect(Math.abs((await scrollY(page)) - before)).toBeLessThan(5);
    if (close === 'button') await action(page, 'project.preview-close').click(); else await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toBeHidden();
    await expect.poll(async () => Math.abs((await scrollY(page)) - before)).toBeLessThan(5);
    await expect.poll(() => onScreen(page, same)).toBe(true);
  }
  // 2. Preview -> full detail -> in-app return.
  if (mobile) await target.click(); else await target.locator('td.project-cell').click();
  await action(page, 'project.detail').click();
  await expect(page.getByRole('heading', { name, exact: true })).toBeVisible();
  await action(page, 'nav.open').filter({ hasText: '返回项目中心' }).click();
  await expect(page.locator('.pagination')).toContainText('第 2 页');
  await expect.poll(async () => Math.abs((await scrollY(page)) - before)).toBeLessThan(60);
  await expect.poll(() => onScreen(page, same)).toBe(true);
  // 3. Preview -> full detail -> browser back.
  if (mobile) await same.click(); else await same.locator('td.project-cell').click();
  await action(page, 'project.detail').click();
  await expect(page.getByRole('heading', { name, exact: true })).toBeVisible();
  await page.goBack();
  await expect(page.locator('.pagination')).toContainText('第 2 页');
  await expect.poll(async () => Math.abs((await scrollY(page)) - before)).toBeLessThan(60);
  await expect.poll(() => onScreen(page, same)).toBe(true);
  // 4. Focus returns to the project the user opened.
  if (mobile) await same.click(); else await same.locator('td.project-cell').click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toBeHidden();
  await expect.poll(() => page.evaluate(() => document.activeElement?.textContent || '')).toContain(name);
  await expect.poll(async () => Math.abs((await scrollY(page)) - before)).toBeLessThan(5);
});

// The UAT trigger: on a slow network the list is replaced (page change, filter,
// back navigation) while the user is already deep in it and opening a preview.
// Replacing every keyed row made the browser drop its scroll anchor and jump to
// the top. The position must survive the replacement.
test('V1.1 AC-13 深滚动时列表整体刷新（慢网络）不跳回顶部', async ({ page }, info) => {
  test.setTimeout(90000);
  await login(page, 'synthetic-b');
  const tag = uniq('AC13刷');
  for (let i = 0; i < 40; i += 10)
    await Promise.all(Array.from({ length: 10 }, (_, j) => api(page, '/api/v1/projects', 'POST', { projectName: `${tag}-${String(i + j).padStart(2, '0')}`, customer: tag + '客户', salesStage: 'L2_TECHNICAL', version: 1 })));
  const mobile = info.project.name === 'mobile-390';
  const names = page.locator(mobile ? '.mobile-projects strong' : 'td.project-cell');
  await page.goto(`/projects?q=${encodeURIComponent(tag)}&sort=name`);
  await expect(names.first()).toHaveText(tag + '-00');
  let release: () => void = () => {};
  await page.route(/\/api\/v1\/projects\?page=2/, async (r) => { await new Promise<void>((ok) => (release = ok)); await r.continue(); });
  await action(page, 'project.page-next').click();
  // While page 2 is still loading: scroll deep and open a preview.
  await page.evaluate(() => window.scrollTo(0, 835));
  await expect.poll(() => scrollY(page)).toBeGreaterThan(600);
  const before = await scrollY(page);
  const rows = page.locator(mobile ? '.mobile-projects [data-action-id="project.preview"]' : '.table-wrap tbody tr');
  const mid = await rows.evaluateAll((els) => { const c = innerHeight / 2; let b = 0, d = 1e9; els.forEach((e, i) => { const r = e.getBoundingClientRect(); const x = Math.abs(r.top + r.height / 2 - c); if (x < d) { d = x; b = i; } }); return b; });
  if (mobile) await rows.nth(mid).click(); else await rows.nth(mid).locator('td.project-cell').click();
  await expect(page.getByRole('dialog')).toBeVisible();
  release();
  await expect(names.first()).toHaveText(tag + '-20');
  await page.waitForTimeout(300);
  expect(Math.abs((await scrollY(page)) - before)).toBeLessThan(5);
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toBeHidden();
  await expect.poll(async () => Math.abs((await scrollY(page)) - before)).toBeLessThan(5);
});
