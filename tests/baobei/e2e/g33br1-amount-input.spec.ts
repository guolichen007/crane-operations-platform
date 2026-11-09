import {
  test,
  expect,
  type Page,
  type Request,
} from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
// G3-3B-R1 P1-B: the estimated amount typed in the browser is stored exactly
// (column = document = text) or refused visibly; an invalid box never lets
// the previous valid amount be saved. Real API, isolated PostgreSQL.
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
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': me.csrfToken, 'Idempotency-Key': 'r1-' + crypto.randomUUID() },
        ...(body === undefined ? {} : typeof body === 'string' ? { body } : { body: JSON.stringify(body) }),
      });
      return { status: r.status, body: await r.json() };
    },
    { path, method, body }
  );
}

test('G3-3B-R1 预估金额：非法输入保留原文、明确报错、不保存旧值；修正后精确保存', async ({ page }, info) => {
  await login(page, 'synthetic-a');
  await action(page, 'project.create').first().click();
  const name = `R1金额-${info.project.name}-${Date.now()}`;
  await page.locator('[name=customer]').fill(name + '客户');
  await page.locator('[name=projectName]').fill(name);
  await action(page, 'project.step-next').click();
  await action(page, 'project.step-next').click();
  const box = page.locator('[name=estimatedAmount]');
  const saveResp = () => page.waitForResponse((r) => /\/api\/v1\/projects(\/[0-9a-f-]+)?$/.test(new URL(r.url()).pathname) && ['POST', 'PATCH'].includes(r.request().method()));
  // 1) A valid amount is saved exactly.
  await box.fill('125000.25');
  let done = saveResp();
  await action(page, 'project.save-draft').click();
  const first = await done;
  expect(first.status()).toBe(200);
  const id = (await first.json()).id as string;
  let r = await api(page, '/api/v1/projects/' + id);
  expect(r.body.estimatedAmount).toBe(125000.25);
  expect(r.body.knowledge.estimatedAmount).toBe('KNOWN');
  // 2) Three decimals: the text stays, a clear error shows, save/submit are
  //    blocked and no request carries the old 125000.25.
  const sent: string[] = [];
  const watch = (req: Request) => {
    if (['POST', 'PATCH'].includes(req.method()) && /\/api\/v1\/projects/.test(req.url())) sent.push(req.postData() || '');
  };
  page.on('request', watch);
  for (const [text, msg] of [['123.456', '最多保留两位小数'], ['90071992547409.91', '无法精确保存到分'], ['100000000000000.01', '超出允许范围'], ['-5', '不小于 0']] as const) {
    await box.fill(text);
    await expect(box).toHaveValue(text);
    await expect(page.locator('.num-field').filter({ has: box }).getByRole('alert')).toContainText(msg);
    await expect(action(page, 'project.save-draft')).toBeDisabled();
    await expect(page.locator('.save-state')).toHaveText('数值有误，未保存');
  }
  r = await api(page, '/api/v1/projects/' + id);
  expect(r.body.estimatedAmount).toBe(125000.25);
  // 3) Correct it: the very next write (autosave or button) carries the new
  //    exact value — no write with the old amount happened in between.
  await box.fill('90071992547409.90');
  await expect(page.locator('.num-field').filter({ has: box }).getByRole('alert')).toHaveCount(0);
  done = saveResp();
  await action(page, 'project.save-draft').click();
  expect((await done).status()).toBe(200);
  page.off('request', watch);
  expect(sent.length).toBeGreaterThan(0);
  for (const body of sent) expect(JSON.parse(body).estimatedAmount).toBe(90071992547409.9);
  r = await api(page, '/api/v1/projects/' + id);
  expect(r.body.estimatedAmount).toBe(90071992547409.9);
  await box.fill('');
  done = saveResp();
  await action(page, 'project.save-draft').click();
  expect((await done).status()).toBe(200);
  r = await api(page, '/api/v1/projects/' + id);
  expect(r.body.estimatedAmount).toBeNull();
  expect(r.body.knowledge.estimatedAmount).toBe('UNKNOWN');
  // 4) Direct API bypassing the browser is refused by the server.
  const bypass = await api(page, '/api/v1/projects/' + id, 'PATCH', `{"version":${r.body.version},"projectName":"${name}","customer":"${name}客户","estimatedAmount":0.125,"knowledge":{"estimatedAmount":"KNOWN"}}`);
  expect(bypass.status).toBe(400);
  expect(bypass.body.message).toContain('最多保留两位小数');
  expect((await api(page, '/api/v1/projects/' + id)).body.version).toBe(r.body.version);
});

test('G3-3B-R1 历史异常金额：打开即提示，修正或改为未知后可保存', async ({ page }, info) => {
  await login(page, 'synthetic-a');
  // A pre-R1 record with a 4-decimal amount cannot be created through the
  // API any more; open an existing draft and simulate its server copy.
  const name = `R1历史金额-${info.project.name}-${Date.now()}`;
  const created = await api(page, '/api/v1/projects', 'POST', { projectName: name, customer: name + '客户', estimatedAmount: 1, knowledge: { estimatedAmount: 'KNOWN' } });
  expect(created.status).toBe(200);
  await page.route(`**/api/v1/projects/${created.body.id}`, async (route) => {
    if (route.request().method() !== 'GET') return route.continue();
    const res = await route.fetch();
    const json = await res.json();
    json.estimatedAmount = 12345678.9999; // what the pre-R1 API stored in the document
    await route.fulfill({ response: res, json });
  });
  await page.goto(`/projects/${created.body.id}/edit`);
  await action(page, 'project.step-jump').filter({ hasText: '商务' }).first().click();
  const box = page.locator('[name=estimatedAmount]');
  await expect(box).toHaveValue('12345678.9999');
  await expect(page.locator('.num-field').filter({ has: box }).getByRole('alert')).toContainText('最多保留两位小数');
  await expect(action(page, 'project.save-draft')).toBeDisabled();
  await page.unroute(`**/api/v1/projects/${created.body.id}`);
  await box.fill('12345679.00');
  const done = page.waitForResponse((r) => r.url().endsWith('/api/v1/projects/' + created.body.id) && r.request().method() === 'PATCH');
  await action(page, 'project.save-draft').click();
  expect((await done).status()).toBe(200);
  expect((await api(page, '/api/v1/projects/' + created.body.id)).body.estimatedAmount).toBe(12345679);
});
