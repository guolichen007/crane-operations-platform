import { test, expect, action, api, uniq } from './support/isolation';
import type { Browser, Page } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';

// V1.1 R7-D browser counter-examples: director-only route (administrator
// refused, no change), team-safe customer names on every page of the other
// team, and a stale own-name page after a supervisor correction.
async function session(browser: Browser, subject: string, baseURL: string | undefined) {
  const ctx = await browser.newContext({ baseURL });
  const p = await ctx.newPage();
  await p.goto('/login');
  await p.locator('select').selectOption(subject);
  await action(p, 'auth.dev-login').click();
  await expect(p.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
  return p;
}

test('V1.1 R7 管理员不能审核主管本人报备（总监路由），状态不变', async ({ browser, baseURL, cleanup }) => {
  const ma = await session(browser, 'synthetic-manager', baseURL);
  const name = uniq('R7总监路由');
  const r = await api(ma, '/api/v1/projects', 'POST', {
    projectName: name, customer: name + '客户', salesStage: 'L1_REQUIREMENT', version: 1,
    location: { province: '合成省', city: '合成市', factoryName: name, workshop: '一车间' }, projectType: 'RETROFIT',
    informationSource: 'END_CUSTOMER', confidenceLevel: 'CUSTOMER_CONFIRMED', requirements: ['ANTI_SWAY'], requirementNotes: '合成', plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 1 }],
  });
  expect(r.status).toBe(200);
  cleanup.track(r.body.id);
  const s = await api(ma, `/api/v1/projects/${r.body.id}/submit`, 'POST', { version: r.body.version });
  expect(s.status).toBe(200);
  await ma.context().close();
  const admin = await session(browser, 'synthetic-admin', baseURL);
  await admin.goto('/projects/' + r.body.id);
  await action(admin, 'project.tab').filter({ hasText: '审批和重复风险' }).click();
  await expect(admin.locator('.review-eligibility')).toContainText('只能由销售总监审核');
  const before = (await api(admin, '/api/v1/projects/' + r.body.id)).body;
  for (const a of ['approve', 'return', 'reject']) {
    const d = await api(admin, `/api/v1/projects/${r.body.id}/${a}`, 'POST', { version: before.version, reason: '管理员越权尝试' });
    expect(d.status).toBe(403);
    expect(d.body.code).toBe('REVIEW_ROUTE_DIRECTOR');
  }
  const after = (await api(admin, '/api/v1/projects/' + r.body.id)).body;
  expect([after.version, after.intakeStatus, after.documentHash]).toEqual([before.version, before.intakeStatus, before.documentHash]);
  await admin.context().close();
});

test('V1.1 R7 另一团队在客户中心、搜索、项目详情都看不到本团队的客户拼写', async ({ browser, baseURL }) => {
  const tag = uniq('R7名称').replace(/-/g, '');
  const spellA = tag + '·北岭重工（集团）有限公司';
  const spellB = tag.toLowerCase() + ' 北岭重工 集团 有限公司';
  const a = await session(browser, 'synthetic-a', baseURL);
  expect((await api(a, '/api/v1/projects', 'POST', { projectName: tag + 'A', customer: spellA, version: 1 })).status).toBe(200);
  await a.context().close();
  const c = await session(browser, 'synthetic-c', baseURL);
  const pb = await api(c, '/api/v1/projects', 'POST', { projectName: tag + 'B', customer: spellB, version: 1 });
  expect(pb.status).toBe(200);
  for (const q of [tag, '北岭重工', '集团']) {
    await c.goto('/customers?q=' + encodeURIComponent(q));
    await expect(c.locator('.customer-list, .empty').first()).toBeVisible();
    await expect(c.locator('body')).not.toContainText(spellA);
  }
  await c.goto('/customers?q=' + encodeURIComponent(tag));
  await expect(action(c, 'customer.open').filter({ hasText: spellB })).toBeVisible();
  await action(c, 'customer.open').filter({ hasText: spellB }).click();
  await expect(c.getByRole('heading', { name: spellB })).toBeVisible();
  await expect(c.locator('body')).not.toContainText(spellA);
  await c.goto('/projects/' + pb.body.id);
  await expect(c.locator('.project-contacts')).toContainText(spellB);
  await expect(c.locator('body')).not.toContainText(spellA);
  // Wildcards are literal.
  const wild = await api(c, '/api/v1/customers?q=' + encodeURIComponent('%'));
  expect(JSON.stringify(wild.body)).not.toContain(tag);
  await c.context().close();
});

test('V1.1 R7 主管更正姓名后，本人旧页面提交被拒绝', async ({ browser, baseURL }) => {
  const b = await session(browser, 'synthetic-c', baseURL);
  await b.goto('/profile');
  await expect(b.locator('[name=displayName]')).toBeVisible();
  const original = await b.locator('[name=displayName]').inputValue();
  const admin = await session(browser, 'synthetic-admin', baseURL);
  const list = await api(admin, '/api/v1/staff?q=' + encodeURIComponent(original));
  const m = list.body.items.find((x: { name: string }) => x.name === original);
  const detail = (await api(admin, '/api/v1/staff/' + m.id)).body.member;
  const fixed = await api(admin, '/api/v1/staff/' + m.id, 'PATCH', { name: original + '（更正）', phone: '', email: detail.email, teamId: detail.teamId, role: detail.role, note: detail.note, reason: '管理员更正', version: detail.version });
  expect(fixed.status).toBe(200);
  await b.locator('[name=displayName]').fill('旧页面提交');
  await action(b, 'profile.save').click();
  await expect(b.locator('#name-error')).toContainText('其他页面更新');
  const now = (await api(b, '/api/v1/me/profile')).body;
  expect(now.name).toBe(original + '（更正）');
  // Restore the fixture name.
  expect((await api(b, '/api/v1/me/profile', 'PATCH', { name: original, reason: '测试复原', version: now.version })).status).toBe(200);
  await admin.context().close();
  await b.context().close();
});
