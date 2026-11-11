import { fileURLToPath } from 'node:url';
import {
  test,
  expect,
  type Page,
} from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
// G2-5 evidence: run on a fresh database with BAOBEI_EVIDENCE=1. Screenshots and
// traces go to the ignored local-private directory; each image is paired with
// HTTP and database assertions in the same test.
test.skip(!process.env.BAOBEI_EVIDENCE, 'evidence run only');
test.describe.configure({ mode: 'serial' });
test.setTimeout(120000);
// Always keep traces for the evidence run, independent of the suite default.
test.use({ trace: 'on' });
const out = (name: string) => fileURLToPath(new URL(`../../../local-private/baobei/g2-evidence/${name}.png`, import.meta.url));
const action = (p: Page, id: string) => p.locator(`[data-action-id="${id}"]:visible`);
const widths = [
  ['1440', 1440, 900],
  ['1366', 1366, 768],
  ['768', 768, 1024],
  ['390', 390, 844],
] as const;
async function login(p: Page, subject: string) {
  await p.context().clearCookies();
  await p.goto('/login');
  await p.locator('select').selectOption(subject);
  await action(p, 'auth.dev-login').click();
  await expect(p.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
}
async function api(p: Page, path: string, method = 'GET', body?: unknown) {
  return p.evaluate(
    async ({ path, method, body }) => {
      const me = await (await fetch('/api/v1/me')).json();
      const r = await fetch(path, {
        method,
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': me.csrfToken, 'Idempotency-Key': 'evidence-' + crypto.randomUUID() },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      return { status: r.status, body: await r.json() };
    },
    { path, method, body }
  );
}
async function homeShots(p: Page, who: string, state: string) {
  for (const [label, w, h] of widths) {
    await p.setViewportSize({ width: w, height: h });
    await p.goto('/');
    await expect(p.locator('.task-board, .empty').first()).toBeVisible();
    await p.waitForTimeout(150);
    expect(await p.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);
    await p.screenshot({ path: out(`home-${who}-${state}-${label}`) });
  }
}
const full = (name: string, extra: Record<string, unknown> = {}) => ({
  projectName: name,
  customer: name + ' 有限公司',
  location: { province: '江苏省', city: '无锡市', factoryName: '合成二厂', workshop: '炼钢车间' },
  projectType: 'RETROFIT',
  informationSource: 'END_CUSTOMER',
  confidenceLevel: 'CUSTOMER_CONFIRMED',
  requirements: ['ANTI_SWAY', 'POSITIONING'],
  requirementNotes: '合成：桥式行车防摇与精准定位改造',
  plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 2, capacityTons: 32, spanM: 22.5, knowledge: { capacityTons: 'KNOWN', spanM: 'KNOWN' } }],
  version: 1,
  ...extra,
});

test('E1 homes with no data: sales and admin at four widths', async ({ page }) => {
  await login(page, 'synthetic-a');
  expect((await api(page, '/api/v1/workspace-summary')).body.counts.total).toBe(0);
  await homeShots(page, 'sales', 'empty');
  await login(page, 'synthetic-admin');
  expect((await api(page, '/api/v1/workspace-summary')).body.counts.total).toBe(0);
  await homeShots(page, 'admin', 'empty');
});

test('E2 admin full path with zero sales: draft → assign → submit → blocked self-review → independent manager', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await login(page, 'synthetic-admin');
  await action(page, 'project.create').first().click();
  await page.locator('[name=customer]').fill('合成重工 有限公司');
  await page.locator('[name=projectName]').fill('三号跨桥式行车自动化改造');
  await action(page, 'project.save-draft').click();
  await expect(page.locator('.owner-strip')).toContainText('待分配');
  await page.screenshot({ path: out('flow-admin-01-draft-unassigned'), fullPage: true });
  await page.locator('[name=province]').fill('江苏省');
  await page.locator('[name=city]').fill('无锡市');
  await page.locator('[name=informationSource]').selectOption('END_CUSTOMER');
  await action(page, 'project.step-next').click();
  await page.getByLabel('定位', { exact: true }).first().check();
  await page.locator('[name=requirementNotes]').fill('合成：精准定位与远程监测');
  await action(page, 'equipment.add').click();
  await page.locator('[name=quantity0]').fill('2');
  await page.locator('[name=capacityTons0]').fill('20');
  await page.locator('[name=spanM0]').fill('22.5');
  await page.screenshot({ path: out('flow-admin-02-equipment'), fullPage: true });
  await action(page, 'project.step-next').click();
  await page.locator('[name=projectType]').selectOption('RETROFIT');
  await page.locator('[name=confidenceLevel]').selectOption('TECHNICAL_CONFIRMED');
  await action(page, 'project.step-next').click();
  await page.locator('[name=assignTarget]').selectOption({ label: '示例销售 A' });
  await page.screenshot({ path: out('flow-admin-03-review-assign'), fullPage: true });
  await action(page, 'project.assign').click();
  await expect(page.locator('.owner-strip')).toContainText('示例销售 A');
  await action(page, 'project.submit').click();
  await expect(page).toHaveURL(/\/projects\/[0-9a-f-]+$/);
  const id = new URL(page.url()).pathname.split('/').pop()!;
  await action(page, 'project.tab').filter({ hasText: '审批和重复风险' }).click();
  await expect(page.locator('.review-eligibility')).toContainText('你参与过此项目');
  await page.screenshot({ path: out('flow-admin-04-self-review-blocked'), fullPage: true });
  const denied = await api(page, `/api/v1/projects/${id}/approve`, 'POST', { version: (await api(page, '/api/v1/projects/' + id)).body.version, reason: '证据：自审尝试' });
  expect(denied.status).toBe(403);
  await action(page, 'auth.account').click();
  await action(page, 'auth.logout').click();
  // Wait for the logout to finish: /login redirects to / while the session is still valid.
  await expect(page).toHaveURL('/login');
  await login(page, 'synthetic-manager');
  await page.goto('/projects/' + id);
  await action(page, 'project.tab').filter({ hasText: '审批和重复风险' }).click();
  await page.screenshot({ path: out('flow-manager-05-independent-review'), fullPage: true });
  await action(page, 'review.approve').click();
  await page.locator('[name=governanceReason]').fill('证据：独立主管核验');
  await action(page, 'governance.confirm').click();
  await expect(page.locator('.detail-heading')).toContainText('已批准');
  await action(page, 'project.tab').filter({ hasText: '归属历史' }).click();
  await page.screenshot({ path: out('flow-manager-06-approved-ownership'), fullPage: true });
  const final = (await api(page, '/api/v1/projects/' + id)).body;
  expect(final).toMatchObject({ intakeStatus: 'APPROVED', ownerName: '示例销售 A', createdByName: '示例管理员', submittedByName: '示例管理员' });
});

test('E3 seed data then homes with data, list, follow-ups and detail at four widths', async ({ page }) => {
  await login(page, 'synthetic-a');
  const day = 86400000;
  for (const [i, offset] of [-2, -1, 2, 5, 12].entries()) {
    const p = (await api(page, '/api/v1/projects', 'POST', full(`合成项目${i + 1}`))).body;
    const s = (await api(page, `/api/v1/projects/${p.id}/submit`, 'POST', { version: p.version })).body;
    await api(page, `/api/v1/projects/${p.id}/activities`, 'POST', { version: s.version, type: 'VISIT', occurredAt: new Date().toISOString(), summary: '合成拜访', result: '', nextAction: '提交技术方案', nextFollowupAt: new Date(Date.now() + offset * day).toISOString() });
  }
  await api(page, '/api/v1/projects', 'POST', { projectName: '合成草稿：待补设备参数', customer: '合成机械 有限公司', version: 1 });
  await login(page, 'synthetic-admin');
  await api(page, '/api/v1/projects', 'POST', { projectName: '合成待分配：港口门机远控', customer: '合成港务 有限公司', version: 1 });
  for (const who of ['sales', 'admin'] as const) {
    await login(page, who === 'sales' ? 'synthetic-a' : 'synthetic-admin');
    const s = (await api(page, '/api/v1/workspace-summary')).body;
    expect(s.counts.total).toBeGreaterThan(0);
    await homeShots(page, who, 'data');
  }
  for (const [label, w, h] of widths) {
    await page.setViewportSize({ width: w, height: h });
    await page.goto('/projects');
    await expect(page.locator('h1 small')).not.toHaveText('0');
    await page.screenshot({ path: out(`list-admin-${label}`) });
  }
  await login(page, 'synthetic-a');
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('/followups');
  await expect(page.locator('h1 small')).toHaveText('2');
  await page.screenshot({ path: out('followups-sales-overdue-1440') });
  await action(page, 'followup.window').filter({ hasText: '未来 7 天' }).click();
  await expect(page.locator('h1 small')).toHaveText('2');
  await page.screenshot({ path: out('followups-sales-upcoming-1440') });
  const id = (await api(page, '/api/v1/projects?q=' + encodeURIComponent('合成项目1'))).body.items[0].id;
  for (const [label, w, h] of widths) {
    await page.setViewportSize({ width: w, height: h });
    await page.goto('/projects/' + id);
    await action(page, 'project.tab').filter({ hasText: '设备与技术需求' }).click();
    await expect(page.locator('.equipment-table')).toContainText('32 t');
    await page.screenshot({ path: out(`detail-sales-${label}`), fullPage: true });
  }
});
