import {
  test,
  expect,
  type Page,
  type Browser,
} from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
// G2-R1: concurrent edits by two real sessions. Disjoint changes must both
// survive; same-field changes must be chosen explicitly; nothing is silently
// overwritten. Each test uses its own synthetic owner and project.
const action = (p: Page, id: string) => p.locator(`[data-action-id="${id}"]:visible`);
async function session(browser: Browser, subject: string, viewport: { width: number; height: number } | null) {
  const ctx = await browser.newContext({ baseURL: 'http://127.0.0.1:5188', viewport: viewport ?? undefined });
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
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': me.csrfToken, 'Idempotency-Key': 'r1-' + crypto.randomUUID() },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      return { status: r.status, body: await r.json() };
    },
    { path, method, body }
  );
}
async function saveDraft(p: Page) {
  const done = p.waitForResponse((r) => /\/api\/v1\/projects\/[0-9a-f-]+$/.test(new URL(r.url()).pathname) && r.request().method() === 'PATCH');
  await action(p, 'project.save-draft').click();
  return (await done).status();
}
const step = (p: Page, label: string) => action(p, 'project.step-jump').filter({ hasText: label }).click();
const base = (name: string) => ({
  projectName: name,
  customer: name + ' 客户',
  location: { province: '合成省', city: '合成市' },
  requirementNotes: '原始需求',
  estimatedAmount: 100000,
  knowledge: { estimatedAmount: 'KNOWN' },
  plannedEquipment: [
    { equipmentType: 'BRIDGE', quantity: 2, capacityTons: 20, spanM: 18, knowledge: { quantity: 'KNOWN', capacityTons: 'KNOWN', spanM: 'KNOWN' } },
  ],
  version: 1,
});

test('disjoint concurrent edits are merged without loss', async ({ browser }, info) => {
  const name = `R1合并-${info.project.name}-${Date.now()}`;
  const admin = await session(browser, 'synthetic-admin', info.project.use.viewport ?? null);
  const created = (await api(admin, '/api/v1/projects', 'POST', base(name))).body;
  const assigned = (await api(admin, `/api/v1/projects/${created.id}/ownership-transfer`, 'POST', { version: created.version, reason: 'R1 指派', targetSubject: 'synthetic-b' })).body;
  // A: admin opens the editor on the assigned version.
  await admin.goto(`/projects/${created.id}/edit`);
  await expect(admin.locator('[name=projectName]')).toHaveValue(name);
  // B: sales owner edits other fields and saves first.
  const sales = await session(browser, 'synthetic-b', info.project.use.viewport ?? null);
  const b = (await api(sales, '/api/v1/projects/' + created.id)).body;
  const remote = await api(sales, '/api/v1/projects/' + created.id, 'PATCH', {
    ...base(name),
    version: assigned.version,
    requirementNotes: 'B 补充：需要防爆',
    estimatedAmount: null,
    knowledge: { estimatedAmount: 'UNKNOWN' },
    nextAction: 'B 安排现场勘察',
    plannedEquipment: [{ ...b.plannedEquipment[0], capacityTons: 32, knowledge: { ...b.plannedEquipment[0].knowledge, capacityTons: 'KNOWN' } }],
  });
  expect(remote.status).toBe(200);
  // A changes only the project name and the span, then saves.
  await admin.locator('[name=projectName]').fill(name + '-A改名');
  await step(admin, '设备与技术');
  await admin.locator('[name=spanM0]').fill('22.5');
  expect(await saveDraft(admin)).toBe(409);
  await expect(admin.locator('.conflict-panel')).toBeVisible();
  // No field changed on both sides: nothing to choose, merge is available.
  await expect(admin.locator('.conflict-choice')).toHaveCount(0);
  const merged = admin.waitForResponse((r) => r.request().method() === 'PATCH' && r.url().includes('/api/v1/projects/'));
  await action(admin, 'draft.conflict-merge').click();
  expect((await merged).status()).toBe(200);
  await expect(admin.locator('.conflict-panel')).toHaveCount(0);
  const r = (await api(admin, '/api/v1/projects/' + created.id)).body;
  expect(r.version).toBe(remote.body.version + 1);
  expect(r.projectName).toBe(name + '-A改名');
  expect(r.requirementNotes).toBe('B 补充：需要防爆');
  expect(r.nextAction).toBe('B 安排现场勘察');
  expect(r.estimatedAmount).toBeNull();
  expect(r.knowledge.estimatedAmount).toBe('UNKNOWN');
  expect(r.plannedEquipment[0]).toMatchObject({ capacityTons: 32, spanM: 22.5, quantity: 2 });
});

test('same-field conflicts require an explicit choice per field', async ({ browser }, info) => {
  const name = `R1同字段-${info.project.name}-${Date.now()}`;
  const owner = await session(browser, 'synthetic-c', info.project.use.viewport ?? null);
  const created = (await api(owner, '/api/v1/projects', 'POST', base(name))).body;
  await owner.goto(`/projects/${created.id}/edit`);
  await expect(owner.locator('[name=projectName]')).toHaveValue(name);
  // A second tab of the same owner changes the name and adds a device.
  const other = await api(owner, '/api/v1/projects/' + created.id, 'PATCH', {
    ...base(name),
    projectName: name + '-另一标签',
    plannedEquipment: [...base(name).plannedEquipment, { equipmentType: 'GANTRY', quantity: 1, knowledge: { quantity: 'KNOWN' } }],
    version: created.version,
  });
  expect(other.status).toBe(200);
  await owner.locator('[name=projectName]').fill(name + '-本页');
  expect(await saveDraft(owner)).toBe(409);
  const choice = owner.locator('.conflict-choice').filter({ hasText: '项目名称' });
  await expect(choice).toHaveCount(1);
  await expect(action(owner, 'draft.conflict-merge')).toBeDisabled();
  await choice.getByLabel('使用服务器版本').check();
  const merged = owner.waitForResponse((r) => r.request().method() === 'PATCH' && r.url().includes('/api/v1/projects/'));
  await action(owner, 'draft.conflict-merge').click();
  expect((await merged).status()).toBe(200);
  await expect(owner.locator('.conflict-panel')).toHaveCount(0);
  const r = (await api(owner, '/api/v1/projects/' + created.id)).body;
  expect(r.projectName).toBe(name + '-另一标签');
  expect(r.plannedEquipment.map((e: { equipmentType: string }) => e.equipmentType)).toEqual(['BRIDGE', 'GANTRY']);
  // The removed "overwrite everything" path no longer exists.
  await expect(owner.locator('[data-action-id="draft.conflict-keep"]')).toHaveCount(0);
});

test('a second conflict during merge is compared again against the newer version', async ({ browser }, info) => {
  const name = `R1二次冲突-${info.project.name}-${Date.now()}`;
  const owner = await session(browser, 'synthetic-c', info.project.use.viewport ?? null);
  const created = (await api(owner, '/api/v1/projects', 'POST', base(name))).body;
  await owner.goto(`/projects/${created.id}/edit`);
  await expect(owner.locator('[name=projectName]')).toHaveValue(name);
  const first = await api(owner, '/api/v1/projects/' + created.id, 'PATCH', { ...base(name), notes: '第一次他人修改', version: created.version });
  await owner.locator('[name=projectName]').fill(name + '-本页');
  expect(await saveDraft(owner)).toBe(409);
  await expect(owner.locator('.conflict-panel')).toContainText('第一次他人修改');
  // Before this editor merges, someone saves again.
  const second = await api(owner, '/api/v1/projects/' + created.id, 'PATCH', { ...base(name), notes: '第一次他人修改', tenderNo: 'ZB-002', version: first.body.version });
  expect(second.status).toBe(200);
  const retry = owner.waitForResponse((r) => r.request().method() === 'PATCH' && r.url().includes('/api/v1/projects/'));
  await action(owner, 'draft.conflict-merge').click();
  expect((await retry).status()).toBe(409);
  await expect(owner.locator('.conflict-panel')).toContainText('修订 ' + second.body.version);
  const done = owner.waitForResponse((r) => r.request().method() === 'PATCH' && r.url().includes('/api/v1/projects/'));
  await action(owner, 'draft.conflict-merge').click();
  expect((await done).status()).toBe(200);
  const r = (await api(owner, '/api/v1/projects/' + created.id)).body;
  expect(r).toMatchObject({ projectName: name + '-本页', notes: '第一次他人修改', tenderNo: 'ZB-002', version: second.body.version + 1 });
});

test('reordered devices plus a remote capacity change need a manual whole-list choice', async ({ browser }, info) => {
  const name = `R1.1重排-${info.project.name}-${Date.now()}`;
  const owner = await session(browser, 'synthetic-c', info.project.use.viewport ?? null);
  const two = {
    ...base(name),
    plannedEquipment: [
      { equipmentType: 'BRIDGE', quantity: 2, capacityTons: 20, knowledge: { quantity: 'KNOWN', capacityTons: 'KNOWN' } },
      { equipmentType: 'GANTRY', quantity: 1, capacityTons: 10, knowledge: { quantity: 'KNOWN', capacityTons: 'KNOWN' } },
    ],
  };
  const created = (await api(owner, '/api/v1/projects', 'POST', two)).body;
  await owner.goto(`/projects/${created.id}/edit`);
  await expect(owner.locator('[name=projectName]')).toHaveValue(name);
  // This editor reorders the devices: remove the bridge crane and add it back last.
  await step(owner, '设备与技术');
  await action(owner, 'equipment.remove').first().click();
  await action(owner, 'equipment.add').click();
  await owner.locator('[name=quantity1]').fill('2');
  await owner.locator('[name=capacityTons1]').fill('20');
  // Another session raises the bridge crane (index 0 on the server) to 32 t.
  const other = await api(owner, '/api/v1/projects/' + created.id, 'PATCH', {
    ...two,
    plannedEquipment: [{ ...two.plannedEquipment[0], capacityTons: 32 }, two.plannedEquipment[1]],
    version: created.version,
  });
  expect(other.status).toBe(200);
  expect(await saveDraft(owner)).toBe(409);
  // Without stable device identity there must be one whole-list decision.
  const choice = owner.locator('.conflict-choice').filter({ hasText: '计划设备（增删或顺序变化）' });
  await expect(choice).toHaveCount(1);
  await expect(owner.locator('.conflict-choice').filter({ hasText: '计划设备 1 ·' })).toHaveCount(0);
  await expect(action(owner, 'draft.conflict-merge')).toBeDisabled();
  await choice.getByLabel('使用服务器版本').check();
  const merged = owner.waitForResponse((r) => r.request().method() === 'PATCH' && r.url().includes('/api/v1/projects/'));
  await action(owner, 'draft.conflict-merge').click();
  expect((await merged).status()).toBe(200);
  const r = (await api(owner, '/api/v1/projects/' + created.id)).body;
  expect(r.plannedEquipment.map((e: { equipmentType: string; capacityTons: number }) => [e.equipmentType, e.capacityTons])).toEqual([['BRIDGE', 32], ['GANTRY', 10]]);
});

test('G3-0: two same-type devices swapped while the other session edits model A need a whole-list choice', async ({ browser }, info) => {
  const name = `G30同型-${info.project.name}-${Date.now()}`;
  const owner = await session(browser, 'synthetic-c', info.project.use.viewport ?? null);
  const twin = {
    ...base(name),
    plannedEquipment: [
      { equipmentType: 'BRIDGE', model: 'A', quantity: 1, capacityTons: 20, knowledge: { quantity: 'KNOWN', capacityTons: 'KNOWN' } },
      { equipmentType: 'BRIDGE', model: 'B', quantity: 1, capacityTons: 10, knowledge: { quantity: 'KNOWN', capacityTons: 'KNOWN' } },
    ],
  };
  const created = (await api(owner, '/api/v1/projects', 'POST', twin)).body;
  await owner.goto(`/projects/${created.id}/edit`);
  await expect(owner.locator('[name=projectName]')).toHaveValue(name);
  // Editor 1 swaps the devices by retyping the two rows (same types, models exchanged).
  await step(owner, '设备与技术');
  const rows = owner.locator('.equipment-row');
  await rows.nth(0).getByLabel('型号').fill('B');
  await owner.locator('[name=capacityTons0]').fill('10');
  await rows.nth(1).getByLabel('型号').fill('A');
  await owner.locator('[name=capacityTons1]').fill('20');
  // Editor 2 raises model A to 32 t.
  const other = await api(owner, '/api/v1/projects/' + created.id, 'PATCH', {
    ...twin,
    plannedEquipment: [{ ...twin.plannedEquipment[0], capacityTons: 32 }, twin.plannedEquipment[1]],
    version: created.version,
  });
  expect(other.status).toBe(200);
  expect(await saveDraft(owner)).toBe(409);
  const choice = owner.locator('.conflict-choice').filter({ hasText: '计划设备（增删或顺序变化）' });
  await expect(choice).toHaveCount(1);
  await expect(owner.locator('.conflict-choice').filter({ hasText: '计划设备 1 ·' })).toHaveCount(0);
  await expect(action(owner, 'draft.conflict-merge')).toBeDisabled();
  await choice.getByLabel('使用服务器版本').check();
  // A second remote save before merging: the next PATCH must 409 and re-compare.
  const again = await api(owner, '/api/v1/projects/' + created.id, 'PATCH', {
    ...twin,
    plannedEquipment: [{ ...twin.plannedEquipment[0], capacityTons: 32 }, { ...twin.plannedEquipment[1], capacityTons: null, knowledge: { quantity: 'KNOWN', capacityTons: 'UNKNOWN' } }],
    version: other.body.version,
  });
  expect(again.status).toBe(200);
  const first = owner.waitForResponse((r) => r.request().method() === 'PATCH' && r.url().includes('/api/v1/projects/'));
  await action(owner, 'draft.conflict-merge').click();
  expect((await first).status()).toBe(409);
  await expect(owner.locator('.conflict-panel')).toContainText('修订 ' + again.body.version);
  // Having taken the server's device list, only the newer server changed it
  // since: it is adopted without another question, nothing is guessed.
  await expect(owner.locator('.conflict-choice')).toHaveCount(0);
  const done = owner.waitForResponse((r) => r.request().method() === 'PATCH' && r.url().includes('/api/v1/projects/'));
  await action(owner, 'draft.conflict-merge').click();
  expect((await done).status()).toBe(200);
  const r = (await api(owner, '/api/v1/projects/' + created.id)).body;
  const byModel = Object.fromEntries(r.plannedEquipment.map((e: { model: string; capacityTons: number | null; knowledge: Record<string, string> }) => [e.model, [e.capacityTons, e.knowledge.capacityTons]]));
  expect(byModel).toEqual({ A: [32, 'KNOWN'], B: [null, 'UNKNOWN'] });
});
