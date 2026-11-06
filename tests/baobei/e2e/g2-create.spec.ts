import {
  test,
  expect,
  type Page,
} from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
// G2-2: progressive intake against the real API and isolated PostgreSQL.
const action = (p: Page, id: string) => p.locator(`[data-action-id="${id}"]:visible`);
async function login(p: Page, subject: string) {
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
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': me.csrfToken, 'Idempotency-Key': 'e2e-' + crypto.randomUUID() },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      return { status: r.status, body: await r.json() };
    },
    { path, method, body }
  );
}
// Wait for the real save round-trip instead of a status text that a previous toast may still show.
async function saveDraft(p: Page) {
  const done = p.waitForResponse((r) => /\/api\/v1\/projects(\/[0-9a-f-]+)?$/.test(new URL(r.url()).pathname) && ['POST', 'PATCH'].includes(r.request().method()));
  await action(p, 'project.save-draft').click();
  expect((await done).status()).toBe(200);
}
const unique = (info: { project: { name: string } }, label: string) => `G2${label}-${info.project.name}-${Date.now()}`;
const draftId = (p: Page) => new URL(p.url()).searchParams.get('draft') || p.url().split('/').at(-2)!;

test('admin unassigned draft, real assignment, then admin cannot review own participation', async ({ page }, info) => {
  await login(page, 'synthetic-admin');
  await action(page, 'project.create').first().click();
  await expect(page.getByRole('heading', { name: '新建项目' })).toBeVisible();
  const name = unique(info, '管理员代录');
  await page.locator('[name=customer]').fill(name + '客户');
  await expect(page.getByText('信息不足，尚无法完成查重').first()).toBeVisible();
  await page.locator('[name=projectName]').fill(name);
  await action(page, 'project.save-draft').click();
  await expect(page.getByRole('status').filter({ hasText: '草稿已保存' }).first()).toBeVisible();
  await expect(page.locator('.owner-strip')).toContainText('待分配');
  const id = draftId(page);
  let r = await api(page, '/api/v1/projects/' + id);
  expect(r.body).toMatchObject({ ownerSubject: null, teamId: null, assignmentStatus: 'UNASSIGNED_DRAFT', intakeStatus: 'DRAFT', createdByName: '示例管理员' });
  // Complete the draft through the four steps.
  await page.locator('[name=province]').fill('合成省');
  await page.locator('[name=city]').fill('合成市');
  await page.locator('[name=informationSource]').selectOption('END_CUSTOMER');
  await action(page, 'project.step-next').click();
  await page.getByLabel('防摇', { exact: true }).first().check();
  await page.locator('[name=requirementNotes]').fill('合成管理员代录需求');
  await action(page, 'equipment.add').click();
  await page.locator('[name=quantity0]').fill('2');
  await page.locator('[name=capacityTons0]').fill('32');
  await action(page, 'project.step-next').click();
  await page.locator('[name=projectType]').selectOption('RETROFIT');
  await page.locator('[name=confidenceLevel]').selectOption('CUSTOMER_CONFIRMED');
  await action(page, 'project.step-next').click();
  await expect(page.getByText('必填信息已齐全。')).toBeVisible();
  // Unassigned cannot be formally submitted: server hint is shown.
  await expect(action(page, 'project.submit')).toBeDisabled();
  await expect(action(page, 'project.submit')).toHaveAttribute('title', /指派/);
  await page.locator('[name=assignTarget]').selectOption({ label: '示例销售 A' });
  await action(page, 'project.assign').click();
  await expect(page.locator('.owner-strip')).toContainText('示例销售 A');
  r = await api(page, '/api/v1/projects/' + id);
  expect(r.body).toMatchObject({ ownerSubject: 'synthetic-a', ownerName: '示例销售 A', createdBySubject: 'synthetic-admin' });
  expect(r.body.plannedEquipment[0]).toMatchObject({ quantity: 2, capacityTons: 32, spanM: null, liftHeightM: null });
  expect(r.body.plannedEquipment[0].knowledge).toMatchObject({ capacityTons: 'KNOWN', spanM: 'UNKNOWN' });
  await expect(action(page, 'project.submit')).toBeEnabled();
  await action(page, 'project.submit').click();
  await expect(page).toHaveURL(/\/projects\/[0-9a-f-]+$/);
  r = await api(page, '/api/v1/projects/' + id);
  expect(r.body.intakeStatus).toMatch(/SUBMITTED|UNDER_REVIEW/);
  expect(r.body.submittedByName).toBe('示例管理员');
  expect(r.body.actions.approve).toMatchObject({ allowed: false, reasonCode: 'REVIEW_CONFLICT' });
  const denied = await api(page, `/api/v1/projects/${id}/approve`, 'POST', { version: r.body.version, reason: '合成自审尝试' });
  expect(denied.status).toBe(403);
});

test('lost create response is recovered by receipt after retry and after refresh', async ({ page }, info) => {
  await login(page, 'synthetic-a');
  await action(page, 'project.create').first().click();
  const name = unique(info, '回执恢复');
  let lose = true;
  const creates: string[] = [];
  await page.route('**/api/v1/projects', async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    creates.push(route.request().headers()['idempotency-key']);
    if (!lose) return route.continue();
    await route.fetch(); // the server commits the project …
    await route.abort('failed'); // … but the browser never sees the response.
  });
  await page.locator('[name=customer]').fill(name);
  await page.locator('[name=projectName]').fill(name);
  await action(page, 'project.save-draft').click();
  await expect(page.getByRole('alert')).toContainText('网络连接失败');
  await expect(page.locator('[name=projectName]')).toHaveValue(name);
  // Retry in-page: the receipt is queried first, no second project is created.
  lose = false;
  await action(page, 'project.save-draft').click();
  await expect(page.getByRole('status').filter({ hasText: '草稿已保存' }).first()).toBeVisible();
  expect(creates.length).toBe(1);
  let list = await api(page, '/api/v1/projects?q=' + encodeURIComponent(name));
  expect(list.body.total).toBe(1);

  // Second intent: lose the response, then refresh the tab.
  await page.goto('/new');
  const second = name + '-刷新';
  lose = true;
  await page.locator('[name=customer]').fill(second);
  await page.locator('[name=projectName]').fill(second);
  await action(page, 'project.save-draft').click();
  await expect(page.getByRole('alert')).toContainText('网络连接失败');
  lose = false;
  page.once('dialog', (d) => d.accept());
  await page.reload();
  await expect(page).toHaveURL(/\/new\?draft=[0-9a-f-]+$/);
  await expect(page.locator('[name=projectName]')).toHaveValue(second);
  await expect(page.getByRole('status').filter({ hasText: '已找回' }).first()).toBeVisible();
  list = await api(page, '/api/v1/projects?q=' + encodeURIComponent(second));
  expect(list.body.total).toBe(1);
  expect(new Set(creates).size).toBe(2);
  // Another account cannot use the receipt.
  const key = creates.at(-1)!;
  const own = await api(page, '/api/v1/project-create-receipts/' + key);
  expect(own.status).toBe(200);
  await action(page, 'auth.logout').click();
  await login(page, 'synthetic-b');
  const other = await api(page, '/api/v1/project-create-receipts/' + key);
  expect(other.status).toBe(404);
});

test('unknown stays null, legacy zero is not silently confirmed', async ({ page }, info) => {
  await login(page, 'synthetic-a');
  const name = unique(info, '历史零值');
  // A legacy client payload: zero without knowledge flags.
  const created = await api(page, '/api/v1/projects', 'POST', { projectName: name, customer: name, estimatedAmount: 0, version: 1 });
  expect(created.status).toBe(200);
  expect(created.body.knowledge.estimatedAmount).toBe('LEGACY_UNCONFIRMED');
  await page.goto(`/projects/${created.body.id}/edit`);
  await action(page, 'project.step-jump').filter({ hasText: '商务与跟进' }).click();
  await expect(page.getByText('历史记录为 0，尚未确认是否真实')).toBeVisible();
  await page.locator('[name=draftNextAction]').fill('合成核实金额');
  await saveDraft(page);
  let r = await api(page, '/api/v1/projects/' + created.body.id);
  expect(r.body).toMatchObject({ estimatedAmount: 0, nextAction: '合成核实金额' });
  expect(r.body.knowledge.estimatedAmount).toBe('LEGACY_UNCONFIRMED');
  await action(page, 'number.set-unknown').click();
  await saveDraft(page);
  r = await api(page, '/api/v1/projects/' + created.body.id);
  expect(r.body.estimatedAmount).toBeNull();
  expect(r.body.knowledge.estimatedAmount).toBe('UNKNOWN');
  await page.locator('[name=estimatedAmount]').fill('0');
  await saveDraft(page);
  r = await api(page, '/api/v1/projects/' + created.body.id);
  expect(r.body.estimatedAmount).toBe(0);
  expect(r.body.knowledge.estimatedAmount).toBe('KNOWN');
});

test('409 stops autosave, keeps input, shows differences and recovers', async ({ page }, info) => {
  await login(page, 'synthetic-a');
  const name = unique(info, '版本冲突');
  const created = await api(page, '/api/v1/projects', 'POST', { projectName: name, customer: name, version: 1 });
  await page.goto(`/projects/${created.body.id}/edit`);
  await expect(page.locator('[name=projectName]')).toHaveValue(name);
  // Another tab updates the same draft.
  const other = await api(page, '/api/v1/projects/' + created.body.id, 'PATCH', { projectName: name, customer: name + '-他人修改', version: created.body.version });
  expect(other.status).toBe(200);
  await page.locator('[name=projectName]').fill(name + '-我的修改');
  await action(page, 'project.save-draft').click();
  await expect(page.locator('.conflict-panel')).toBeVisible();
  await expect(page.locator('.conflict-panel')).toContainText('他人修改');
  await expect(page.locator('[name=projectName]')).toHaveValue(name + '-我的修改');
  await expect(action(page, 'project.save-draft')).toBeDisabled();
  // Disjoint fields: my name and the other tab's customer are both kept.
  await expect(page.locator('.conflict-choice')).toHaveCount(0);
  const merged = page.waitForResponse((r) => r.request().method() === 'PATCH' && r.url().includes('/api/v1/projects/'));
  await action(page, 'draft.conflict-merge').click();
  expect((await merged).status()).toBe(200);
  await expect(page.locator('.conflict-panel')).toHaveCount(0);
  const r = await api(page, '/api/v1/projects/' + created.body.id);
  expect(r.body.projectName).toBe(name + '-我的修改');
  expect(r.body.customer).toBe(name + '-他人修改');
  expect(r.body.version).toBe(other.body.version + 1);
});
