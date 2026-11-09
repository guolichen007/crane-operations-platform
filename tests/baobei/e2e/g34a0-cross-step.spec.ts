import { type Page, type Request } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
import { test, expect, uniq } from './support/isolation';
// G3-4-A0 gate 1: an invalid number that was not corrected must never be
// dropped silently when the user changes step, jumps, goes back, leaves or
// submits — and the previously saved value must not be (re)written as if the
// user had accepted it. Real API, desktop and 390 px.
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
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': me.csrfToken, 'Idempotency-Key': 'a0-' + crypto.randomUUID() },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      return { status: r.status, body: await r.json() };
    },
    { path, method, body }
  );
}
const writes = (p: Page) => {
  const sent: { method: string; body: Record<string, unknown> }[] = [];
  p.on('request', (r: Request) => {
    if (['POST', 'PATCH'].includes(r.method()) && /\/api\/v1\/projects(\/[0-9a-f-]+)?(\/submit)?$/.test(new URL(r.url()).pathname))
      sent.push({ method: r.method() + ' ' + new URL(r.url()).pathname.split('/').at(-1), body: JSON.parse(r.postData() || '{}') });
  });
  return sent;
};
const fieldAlert = (p: Page, name: string) => p.locator('.num-field').filter({ has: p.locator(`[name=${name}]`) }).getByRole('alert');

// A complete, submittable draft with a saved amount of 125000.25.
async function prepared(page: Page, tag: string) {
  const r = await api(page, '/api/v1/projects', 'POST', {
    projectName: tag, customer: tag + '客户', location: { province: '合成省', city: '合成市', factoryName: tag, workshop: '一车间' },
    projectType: 'RETROFIT', salesStage: 'L1_REQUIREMENT', informationSource: 'END_CUSTOMER', confidenceLevel: 'CUSTOMER_CONFIRMED',
    requirements: ['ANTI_SWAY'], requirementNotes: '合成需求', plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 2, spanM: 22.5 }],
    estimatedAmount: 125000.25, knowledge: { estimatedAmount: 'KNOWN' },
  });
  expect(r.status).toBe(200);
  await page.goto(`/projects/${r.body.id}/edit`);
  await expect(page.locator('[name=customer]')).toHaveValue(tag + '客户');
  return r.body.id as string;
}
const stepTo = (p: Page, label: string) => action(p, 'project.step-jump').filter({ hasText: label }).first().click();

test('A0-1 下一步：非法金额不能被静默丢弃后保存/提交旧值', async ({ page }, info) => {
  await login(page, 'synthetic-a');
  const id = await prepared(page, `A0下一步-${uniq(info.project.name)}`);
  await stepTo(page, '商务');
  await page.locator('[name=estimatedAmount]').fill('123.456');
  await expect(fieldAlert(page, 'estimatedAmount')).toContainText('最多保留两位小数');
  const sent = writes(page);
  await action(page, 'project.step-next').click();
  // Either navigation is refused with the field still visible, or the review
  // step clearly shows the unresolved field; submitting must not be possible.
  const stillOnField = await page.locator('[name=estimatedAmount]').isVisible();
  if (stillOnField) {
    await expect(page.locator('[name=estimatedAmount]')).toHaveValue('123.456');
    await expect(fieldAlert(page, 'estimatedAmount')).toBeVisible();
  } else {
    await expect(action(page, 'project.submit')).toBeDisabled();
  }
  await expect(page.locator('.wizard')).toContainText('预估金额');
  await expect(action(page, 'project.save-draft')).toBeDisabled();
  expect(sent).toEqual([]);
  expect((await api(page, '/api/v1/projects/' + id)).body.estimatedAmount).toBe(125000.25);
});

test('A0-2 顶部步骤跳转与上一步：同一规则', async ({ page }, info) => {
  await login(page, 'synthetic-a');
  await prepared(page, `A0跳转-${uniq(info.project.name)}`);
  await stepTo(page, '商务');
  await page.locator('[name=estimatedAmount]').fill('100.005');
  const sent = writes(page);
  for (const go of [() => stepTo(page, '核对'), () => stepTo(page, '客户'), () => action(page, 'project.step-back').click()]) {
    await go();
    if (await page.locator('[name=estimatedAmount]').isVisible()) {
      await expect(page.locator('[name=estimatedAmount]')).toHaveValue('100.005');
    } else {
      await expect(action(page, 'project.save-draft')).toBeDisabled();
      if (await action(page, 'project.submit').count()) await expect(action(page, 'project.submit')).toBeDisabled();
      await stepTo(page, '商务');
      await expect(page.locator('[name=estimatedAmount]')).toHaveValue('100.005');
    }
  }
  expect(sent).toEqual([]);
});

test('A0-3 离开整个报备：非法输入属于未保存修改，需要确认', async ({ page }, info) => {
  await login(page, 'synthetic-a');
  await prepared(page, `A0离开-${uniq(info.project.name)}`);
  await stepTo(page, '商务');
  await page.locator('[name=estimatedAmount]').fill('12345678.9999');
  const sent = writes(page);
  await page.getByRole('button', { name: '工作台', exact: true }).first().click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await action(page, 'guard.stay').click();
  await expect(page.locator('[name=estimatedAmount]')).toHaveValue('12345678.9999');
  expect(sent).toEqual([]);
});

test('A0-4 设备字段与移除设备行', async ({ page }, info) => {
  await login(page, 'synthetic-a');
  await prepared(page, `A0设备-${uniq(info.project.name)}`);
  await stepTo(page, '设备');
  await page.locator('[name=quantity0]').fill('2.5');
  await expect(fieldAlert(page, 'quantity0')).toBeVisible();
  await page.locator('[name=spanM0]').fill('-1');
  await expect(fieldAlert(page, 'spanM0')).toBeVisible();
  const sent = writes(page);
  await action(page, 'project.step-next').click();
  if (!(await page.locator('[name=quantity0]').isVisible())) {
    await expect(action(page, 'project.save-draft')).toBeDisabled();
    await stepTo(page, '设备');
  }
  expect(sent).toEqual([]);
  // Removing the row drops its invalid text: no permanent block remains.
  await action(page, 'equipment.remove').first().click();
  await expect(action(page, 'project.save-draft')).toBeEnabled();
  await action(page, 'equipment.add').click();
  await page.locator('[name=quantity0]').fill('3');
  const done = page.waitForResponse((r) => r.request().method() === 'PATCH' && /\/api\/v1\/projects\//.test(r.url()));
  await action(page, 'project.save-draft').click();
  expect((await done).status()).toBe(200);
});

test('A0-5 自动保存进行中输入非法值：不产生新的错误写入', async ({ page }, info) => {
  await login(page, 'synthetic-a');
  const id = await prepared(page, `A0自动保存-${uniq(info.project.name)}`);
  await stepTo(page, '商务');
  // Slow the save so it is in flight while the user types the invalid value.
  await page.route('**/api/v1/projects/' + id, async (route) => {
    if (route.request().method() === 'PATCH') await new Promise((r) => setTimeout(r, 800));
    await route.continue();
  });
  const sent = writes(page);
  await page.locator('[name=draftNextAction]').fill('合成：自动保存中');
  const inflight = page.waitForRequest((r) => r.method() === 'PATCH');
  await inflight; // autosave started (1.2 s debounce)
  await page.locator('[name=estimatedAmount]').fill('0.125');
  await expect(fieldAlert(page, 'estimatedAmount')).toBeVisible();
  await page.waitForResponse((r) => r.request().method() === 'PATCH');
  await action(page, 'project.step-next').click();
  await stepTo(page, '商务').catch(() => undefined);
  expect(sent.length).toBe(1);
  expect(sent[0].body.estimatedAmount).toBe(125000.25); // the in-flight save carried the old value only once
  expect(sent[0].body.nextAction).toBe('合成：自动保存中');
  expect((await api(page, '/api/v1/projects/' + id)).body.estimatedAmount).toBe(125000.25);
});

test('A0-6 历史异常金额：跨步骤后仍需处理', async ({ page }, info) => {
  await login(page, 'synthetic-a');
  const tag = `A0历史-${uniq(info.project.name)}`;
  const id = await prepared(page, tag);
  await page.route(`**/api/v1/projects/${id}`, async (route) => {
    if (route.request().method() !== 'GET') return route.continue();
    const res = await route.fetch();
    const json = await res.json();
    json.estimatedAmount = 12345678.9999;
    await route.fulfill({ response: res, json });
  });
  await page.goto(`/projects/${id}/edit`);
  await stepTo(page, '商务');
  await expect(fieldAlert(page, 'estimatedAmount')).toContainText('最多保留两位小数');
  const sent = writes(page);
  await action(page, 'project.step-next').click();
  if (!(await page.locator('[name=estimatedAmount]').isVisible())) await expect(action(page, 'project.submit')).toBeDisabled();
  expect(sent).toEqual([]);
});

test('A0-7 明确放弃：恢复已保存值后可切换步骤并正常提交', async ({ page, cleanup }, info) => {
  await login(page, 'synthetic-a');
  // Registered before the submit: the fixture returns the filing after the
  // test, also when an assertion after the submit fails (G3-4-A1).
  const id = cleanup.track(await prepared(page, `A0放弃-${uniq(info.project.name)}`));
  await stepTo(page, '商务');
  await page.locator('[name=estimatedAmount]').fill('123.456');
  await action(page, 'project.step-next').click();
  await expect(page.getByRole('alert').filter({ hasText: '放弃修改' }).first()).toBeVisible();
  await action(page, 'number.revert-input').click();
  await expect(page.locator('[name=estimatedAmount]')).toHaveValue('125000.25');
  await expect(fieldAlert(page, 'estimatedAmount')).toHaveCount(0);
  await action(page, 'project.step-next').click();
  await expect(action(page, 'project.submit')).toBeEnabled();
  const sub = page.waitForResponse((r) => r.url().endsWith('/submit'));
  await action(page, 'project.submit').click();
  expect((await sub).status()).toBe(200);
  expect((await api(page, '/api/v1/projects/' + id)).body.estimatedAmount).toBe(125000.25);
});
