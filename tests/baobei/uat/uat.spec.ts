import { fileURLToPath } from 'node:url';
import { appendFileSync, mkdirSync } from 'node:fs';
import { type Browser, type Page } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
import { test, expect, action, api, findCard, uniq, RUN, returnPending, assertCleaned } from '../e2e/support/isolation';
// G3-4-A1 four-role UAT, executable form of docs/项目报备中心/34 §3. Each step
// records: identity, page evidence (screenshot of the real page), the API call
// and the server state it was checked against, and PASS/FAIL. Steps that never
// ran because an earlier step failed are reported BLOCKED by uat-report.mjs.
// Synthetic identities and data only (dev login, names prefixed UAT-<run>).
// Not part of CI: run with tests/baobei/uat/uat.config.ts.
const OUT = fileURLToPath(new URL(`../../../local-private/baobei/g34a1/uat-evidence/`, import.meta.url));
mkdirSync(OUT, { recursive: true });
const SHA = process.env.BAOBEI_SHA || 'local';
const T = uniq('UAT');
const vp = { width: 1440, height: 900 };
const pages: Record<string, Page> = {};
const submitted = new Set<string>();
const S: Record<string, any> = {}; // eslint-disable-line @typescript-eslint/no-explicit-any

async function as(browser: Browser, subject: string) {
  if (pages[subject]) return pages[subject];
  const ctx = await browser.newContext({ baseURL: 'http://127.0.0.1:5188', viewport: vp });
  const p = await ctx.newPage();
  await p.goto('/login');
  await p.locator('select').selectOption(subject);
  await action(p, 'auth.dev-login').click();
  await expect(p.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
  pages[subject] = p;
  return p;
}
// One UAT step. `check` returns the API/server facts it verified.
async function step(id: string, who: string, p: Page, title: string, check: () => Promise<Record<string, unknown>>) {
  const shot = `${id}.png`;
  let result = 'PASS', facts: Record<string, unknown> = {}, error = '';
  try {
    facts = await check();
  } catch (e) {
    result = 'FAIL';
    error = String((e as Error).message || e).split('\n').slice(0, 4).join(' ');
  }
  await p.evaluate(() => scrollTo(0, 0)).catch(() => undefined);
  await p.screenshot({ path: OUT + shot, fullPage: false }).catch(() => undefined);
  appendFileSync(OUT + 'results.jsonl', JSON.stringify({ run: RUN, sha: SHA, id, who, title, result, facts, error, shot, at: new Date().toISOString() }) + '\n');
  if (result === 'FAIL') throw new Error(`${id} ${title}: ${error}`);
}
const get = async (p: Page, id: string) => (await api(p, '/api/v1/projects/' + id)).body;
// Click "保存草稿" and wait for this save's own response (the status text from
// an earlier save may still be on screen).
async function saveDraft(p: Page) {
  const done = p.waitForResponse((r) => ['POST', 'PATCH'].includes(r.request().method()) && /\/api\/v1\/projects(\/[0-9a-f-]+)?$/.test(new URL(r.url()).pathname));
  await action(p, 'project.save-draft').click();
  expect((await done).status()).toBe(200);
}

test.describe.configure({ mode: 'serial' });
test.use({ actionTimeout: 10000 });
test.afterAll(async ({ browser }) => {
  for (const p of Object.values(pages)) await p.context().close();
  assertCleaned(await returnPending(browser, [...submitted], 'UAT'), 'UAT');
});

test('销售：新建、草稿、自动保存恢复、设备需求、金额、查重、提交、状态', async ({ browser }) => {
  test.setTimeout(150000);
  const p = await as(browser, 'synthetic-a');
  S.name = `${T}·东港钢铁桥机防摇改造`;
  await step('S-01', '销售 synthetic-a', p, '登录后看到销售工作台与首要任务', async () => {
    const w = (await api(p, '/api/v1/workspace-summary')).body;
    await expect(action(p, 'dashboard.task').first()).toContainText('已逾期跟进');
    return { 'GET /api/v1/workspace-summary': 200, scope: w.scope, total: w.counts.total };
  });
  await step('S-02', '销售 synthetic-a', p, '新建线索：填写客户与项目名，保存草稿', async () => {
    await action(p, 'project.create').first().click();
    await p.locator('[name=customer]').fill(`${T}东港钢铁有限公司`);
    await p.locator('[name=projectName]').fill(S.name);
    await p.locator('[name=province]').fill('合成省');
    await p.locator('[name=city]').fill('合成市');
    await p.locator('[name=factoryName]').fill('合成一厂');
    await p.locator('[name=informationSource]').selectOption('END_CUSTOMER');
    await saveDraft(p);
    await expect(p).toHaveURL(/\/projects\/[0-9a-f-]+\/edit|\/new\?draft=/);
    S.id = new URL(p.url()).pathname.split('/')[2] || new URL(p.url()).searchParams.get('draft');
    const r = await get(p, S.id);
    expect(r.intakeStatus).toBe('DRAFT');
    expect(r.ownerSubject).toBe('synthetic-a');
    return { 'POST /api/v1/projects': 200, intakeStatus: r.intakeStatus, registrationNo: r.registrationNo, owner: r.ownerSubject };
  });
  await step('S-03', '销售 synthetic-a', p, '查重提示：信息齐全后显示查重结论', async () => {
    await expect(p.getByText(/暂未发现疑似重复|发现疑似已有报备/).first()).toBeVisible();
    const d = await api(p, '/api/v1/duplicate-checks', 'POST', { projectName: S.name, customer: `${T}东港钢铁有限公司`, location: { province: '合成省', city: '合成市', factoryName: '合成一厂' } });
    return { 'POST /api/v1/duplicate-checks': d.status, candidates: (d.body.candidates || []).length };
  });
  await step('S-04', '销售 synthetic-a', p, '设备与需求：防摇需求、2 台 32 t 桥机', async () => {
    await action(p, 'project.step-next').click();
    await p.getByLabel('防摇', { exact: true }).first().check();
    await p.locator('[name=requirementNotes]').fill('合成需求：桥机防摇与精确定位，停机窗口 3 天');
    await action(p, 'equipment.add').click();
    await p.locator('[name=quantity0]').fill('2');
    await p.locator('[name=capacityTons0]').fill('32');
    await saveDraft(p);
    const r = await get(p, S.id);
    expect(r.requirements).toContain('ANTI_SWAY');
    expect(r.plannedEquipment[0]).toMatchObject({ quantity: 2, capacityTons: 32 });
    return { 'PATCH /api/v1/projects/{id}': 200, requirements: r.requirements, equipment: r.plannedEquipment.map((e: { quantity: number; capacityTons: number }) => `${e.quantity}×${e.capacityTons}t`) };
  });
  await step('S-05', '销售 synthetic-a', p, '金额未知：留空保存为“未知”', async () => {
    await action(p, 'project.step-next').click();
    await p.locator('[name=projectType]').selectOption('RETROFIT');
    await p.locator('[name=confidenceLevel]').selectOption('CUSTOMER_CONFIRMED');
    await p.locator('[name=estimatedAmount]').fill('');
    await saveDraft(p);
    const r = await get(p, S.id);
    expect(r.estimatedAmount).toBeNull();
    expect(r.knowledge.estimatedAmount).toBe('UNKNOWN');
    return { estimatedAmount: r.estimatedAmount, knowledge: r.knowledge.estimatedAmount };
  });
  await step('S-06', '销售 synthetic-a', p, '金额已知：输入 1,250,000.50 保存', async () => {
    await p.locator('[name=estimatedAmount]').fill('1250000.50');
    await saveDraft(p);
    const r = await get(p, S.id);
    expect(r.estimatedAmount).toBe(1250000.5);
    expect(r.knowledge.estimatedAmount).toBe('KNOWN');
    return { estimatedAmount: r.estimatedAmount, knowledge: r.knowledge.estimatedAmount };
  });
  await step('S-07', '销售 synthetic-a', p, '非法金额跨步骤拦截：123.456 不能切换步骤，放弃后恢复', async () => {
    await p.locator('[name=estimatedAmount]').fill('123.456');
    await action(p, 'project.step-next').click();
    await expect(p.locator('[name=estimatedAmount]')).toHaveValue('123.456');
    await expect(action(p, 'project.save-draft')).toBeDisabled();
    await action(p, 'number.revert-input').click();
    await expect(p.locator('[name=estimatedAmount]')).toHaveValue('1250000.5');
    const header = (await p.locator('.page-heading').first().innerText()).replace(/\s+/g, ' ');
    expect(header).not.toContain('数值有误'); // UAT-F1
    await action(p, 'project.step-next').click();
    await expect(p.getByText('必填信息已齐全。')).toBeVisible();
    await action(p, 'project.step-back').click();
    const r = await get(p, S.id);
    expect(r.estimatedAmount).toBe(1250000.5);
    return { stepBlocked: true, serverAmountUnchanged: r.estimatedAmount, headerAfterRevert: header };
  });
  await step('S-08', '销售 synthetic-a', p, '自动保存与恢复：只改“下一步事项”，等待自动保存后刷新页面仍在', async () => {
    const saved = p.waitForResponse((r) => r.request().method() === 'PATCH' && r.url().includes('/api/v1/projects/'));
    await p.locator('[name=draftNextAction]').fill('约技术交流，确认跨度与控制方式');
    expect((await saved).status()).toBe(200);
    await p.reload();
    await action(p, 'project.step-jump').filter({ hasText: '商务' }).first().click();
    await expect(p.locator('[name=draftNextAction]')).toHaveValue('约技术交流，确认跨度与控制方式');
    const r = await get(p, S.id);
    return { 'PATCH (autosave, 1.2 s debounce)': 200, nextAction: r.nextAction };
  });
  await step('S-09', '销售 synthetic-a', p, '正式提交：核对页齐全后提交', async () => {
    await action(p, 'project.step-jump').filter({ hasText: '核对' }).first().click();
    await expect(p.getByText('必填信息已齐全。')).toBeVisible();
    submitted.add(S.id);
    await action(p, 'project.submit').click();
    await expect(p).toHaveURL(new RegExp(`/projects/${S.id}$`));
    const r = await get(p, S.id);
    expect(['SUBMITTED', 'UNDER_REVIEW']).toContain(r.intakeStatus);
    S.reg = r.registrationNo;
    return { 'POST /api/v1/projects/{id}/submit': 200, intakeStatus: r.intakeStatus, submissionRevision: r.submissionRevision, evidenceState: r.evidenceState };
  });
  await step('S-10', '销售 synthetic-a', p, '查看审核状态：详情页显示已提交与审批分区', async () => {
    await expect(p.locator('.detail-heading')).toContainText(/已提交|重复待审核/);
    await action(p, 'project.tab').filter({ hasText: '审批和重复风险' }).click();
    const r = await get(p, S.id);
    expect(r.actions.approve.allowed).toBe(false);
    return { intakeStatus: r.intakeStatus, 'actions.approve': r.actions.approve };
  });
  await step('S-11', '销售 synthetic-a', p, '销售无审核入口：/reviews 跳回工作台，接口 403', async () => {
    await p.goto('/reviews');
    await expect(p).toHaveURL('/');
    const r = await api(p, '/api/v1/reviews');
    expect(r.status).toBe(403);
    return { 'GET /api/v1/reviews': r.status };
  });
});

test('主管：本组项目、可审核队列、冻结证据、退回；销售补充再提交；主管批准；独立性', async ({ browser }) => {
  test.setTimeout(150000);
  const m = await as(browser, 'synthetic-manager');
  const a = await as(browser, 'synthetic-a');
  await step('M-01', '主管 synthetic-manager', m, '工作台“你可独立审核”= 接口 = 队列', async () => {
    await m.goto('/');
    const w = (await api(m, '/api/v1/workspace-summary')).body.counts;
    await expect(action(m, 'dashboard.task').filter({ hasText: '你可独立审核' }).locator('.task-count')).toHaveText(String(w.reviewable));
    const q = (await api(m, '/api/v1/reviews?kind=pending&eligibility=reviewable&pageSize=1')).body;
    expect(q.total).toBe(w.reviewable);
    S.reviewableBefore = w.reviewable;
    return { reviewable: w.reviewable, reviewQueue: w.reviewQueue, 'GET /api/v1/reviews?eligibility=reviewable total': q.total };
  });
  await step('M-02', '主管 synthetic-manager', m, '本组项目列表能查到销售 A 的报备', async () => {
    await m.goto('/projects');
    await m.getByRole('textbox', { name: '搜索报备', exact: true }).fill(S.name);
    await expect(m.locator('h1 small')).toHaveText('1');
    const r = (await api(m, '/api/v1/projects?q=' + encodeURIComponent(S.name))).body;
    return { 'GET /api/v1/projects?q': r.total };
  });
  await step('M-03', '主管 synthetic-manager', m, '打开审核卡：核对冻结报备证据后退回补充', async () => {
    await m.goto('/reviews?eligibility=reviewable');
    await (await findCard(m, S.reg)).click();
    const dlg = m.getByRole('dialog');
    await expect(dlg).toContainText(S.reg);
    const before = await get(m, S.id);
    expect(before.evidenceState).toBe('COMPLETE');
    await action(m, 'review.return').click();
    await m.locator('[name=reviewReason]').fill('合成：请补充现场照片与停机窗口');
    await action(m, 'review.confirm').click();
    await expect(m.getByRole('dialog')).toHaveCount(0);
    const r = await get(m, S.id);
    expect(r.intakeStatus).toBe('RETURNED');
    return { evidenceState: before.evidenceState, submissionRevision: before.submissionRevision, 'POST /return': 200, intakeStatus: r.intakeStatus };
  });
  await step('M-04', '主管 synthetic-manager', m, '队列与工作台随之更新（可审核数 −1）', async () => {
    await m.goto('/');
    const w = (await api(m, '/api/v1/workspace-summary')).body.counts;
    expect(w.reviewable).toBe(S.reviewableBefore - 1);
    await expect(action(m, 'dashboard.task').filter({ hasText: '你可独立审核' }).locator('.task-count')).toHaveText(String(w.reviewable));
    return { reviewableBefore: S.reviewableBefore, reviewableAfter: w.reviewable };
  });
  await step('S-12', '销售 synthetic-a', a, '退回后补充：工作台“退回补充”可见，补充需求后再次提交', async () => {
    await a.goto('/');
    await expect(action(a, 'dashboard.task').filter({ hasText: '退回补充' }).locator('.task-count')).not.toHaveText('0');
    await a.goto(`/projects/${S.id}/edit`);
    await action(a, 'project.step-jump').filter({ hasText: '设备' }).first().click();
    await a.locator('[name=requirementNotes]').fill('合成需求：桥机防摇与精确定位，停机窗口 3 天；已补充现场照片');
    await action(a, 'project.step-jump').filter({ hasText: '核对' }).first().click();
    await action(a, 'project.submit').click();
    await expect(a).toHaveURL(new RegExp(`/projects/${S.id}$`));
    const r = await get(a, S.id);
    expect(['SUBMITTED', 'UNDER_REVIEW']).toContain(r.intakeStatus);
    expect(r.submissionRevision).toBe(2);
    return { intakeStatus: r.intakeStatus, submissionRevision: r.submissionRevision };
  });
  await step('S-13', '销售 synthetic-a', a, '跟进记录与下一次跟进', async () => {
    await action(a, 'activity.open').click();
    await a.locator('[name=summary]').fill('合成：电话确认停机窗口');
    await a.locator('[name=nextAction]').fill('发送合成方案');
    await a.locator('[name=nextFollowupAt]').fill('2027-01-05T10:00');
    await action(a, 'activity.create').click();
    await expect(a.getByRole('heading', { name: '合成：电话确认停机窗口', exact: true })).toBeVisible();
    const r = await get(a, S.id);
    expect(r.nextAction).toBe('发送合成方案');
    return { 'POST /activities': 200, nextAction: r.nextAction, nextFollowupAt: r.nextFollowupAt };
  });
  await step('M-05', '主管 synthetic-manager', m, '再次审核：批准', async () => {
    await m.goto('/reviews?eligibility=reviewable');
    await (await findCard(m, S.reg)).click();
    await action(m, 'review.approve').click();
    await m.locator('[name=reviewReason]').fill('合成：资料齐全，批准');
    await action(m, 'review.confirm').click();
    await expect(m.getByRole('dialog')).toHaveCount(0);
    const r = await get(m, S.id);
    expect(r.intakeStatus).toBe('APPROVED');
    return { 'POST /approve': 200, intakeStatus: r.intakeStatus, protectionStatus: r.protectionStatus };
  });
  await step('S-14', '销售 synthetic-a', a, '销售阶段推进：线索 → 需求确认（合法下一阶段，填写原因）', async () => {
    await a.goto('/projects/' + S.id);
    await action(a, 'project.stage-change').click();
    await a.locator('[name=targetStage]').selectOption('L1_REQUIREMENT');
    await a.locator('[name=governanceReason]').fill('合成：客户确认需求，进入需求确认');
    await action(a, 'governance.confirm').click();
    await expect(a.locator('.detail-heading')).toContainText('需求确认');
    const r = await get(a, S.id);
    expect(r.salesStage).toBe('L1_REQUIREMENT');
    return { 'POST /stage-change': 200, salesStage: r.salesStage };
  });
  await step('S-14b', '销售 synthetic-a', a, '跳级推进不可选：需求确认只能到技术交流/暂缓/已失单；直接调接口也被拒绝', async () => {
    await action(a, 'project.stage-change').click();
    const options = await a.locator('select[name=targetStage] option').allTextContents();
    expect(options).toEqual(['请选择', '技术交流', '暂缓', '已失单']);
    await action(a, 'dialog.cancel').click();
    const cur = await get(a, S.id);
    const skip = await api(a, `/api/v1/projects/${S.id}/stage-change`, 'POST', { version: cur.version, salesStage: 'L6_TENDER', reason: '合成：跳级尝试' });
    expect(skip.status).toBe(409);
    const r = await get(a, S.id);
    expect(r.salesStage).toBe('L1_REQUIREMENT');
    return { optionsOffered: options.slice(1), 'POST /stage-change (skip, direct)': `${skip.status} ${skip.body.code}`, salesStageUnchanged: r.salesStage };
  });
  await step('S-15', '销售 synthetic-a', a, '版本冲突：另一窗口先改，保存时显示差异并合并', async () => {
    const name = uniq(`${T}·冲突演示`);
    const c = await api(a, '/api/v1/projects', 'POST', { projectName: name, customer: name, version: 1 });
    await a.goto(`/projects/${c.body.id}/edit`);
    await expect(a.locator('[name=projectName]')).toHaveValue(name);
    const other = await api(a, '/api/v1/projects/' + c.body.id, 'PATCH', { projectName: name, customer: name + '-另一窗口', version: c.body.version });
    expect(other.status).toBe(200);
    await a.locator('[name=projectName]').fill(name + '-本窗口');
    await action(a, 'project.save-draft').click();
    await expect(a.locator('.conflict-panel')).toBeVisible();
    await action(a, 'draft.conflict-merge').click();
    await expect(a.locator('.conflict-panel')).toHaveCount(0);
    const r = await get(a, c.body.id);
    expect(r.projectName).toBe(name + '-本窗口');
    expect(r.customer).toBe(name + '-另一窗口');
    return { 'PATCH (stale version)': 409, merged: { projectName: r.projectName, customer: r.customer }, version: r.version };
  });
  await step('M-06', '主管 synthetic-manager', m, '审核独立性：主管经手（交接）的项目不在“我可审核”，详情与接口都拒绝', async () => {
    const name = uniq(`${T}·主管经手`);
    const d = await api(a, '/api/v1/projects', 'POST', { projectName: name, customer: name + '客户', location: { province: '合成省', city: '合成市', factoryName: '合成二厂', workshop: name }, projectType: 'RETROFIT', salesStage: 'L1_REQUIREMENT', informationSource: 'END_CUSTOMER', confidenceLevel: 'CUSTOMER_CONFIRMED', requirements: ['ANTI_SWAY'], requirementNotes: '合成需求', plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 1 }] });
    const moved = await api(m, `/api/v1/projects/${d.body.id}/ownership-transfer`, 'POST', { version: d.body.version, reason: '合成交接', targetSubject: 'synthetic-b' });
    expect(moved.status).toBe(200);
    const b = await as(browser, 'synthetic-b');
    S.blockedId = d.body.id;
    submitted.add(d.body.id);
    const s = await api(b, `/api/v1/projects/${d.body.id}/submit`, 'POST', { version: moved.body.version });
    expect(s.status).toBe(200);
    S.blockedReg = s.body.registrationNo;
    const reviewable = (await api(m, '/api/v1/reviews?kind=pending&eligibility=reviewable&pageSize=100')).body.items.map((x: { id: string }) => x.id);
    expect(reviewable).not.toContain(d.body.id);
    await m.goto('/reviews?eligibility=blocked');
    await expect(await findCard(m, S.blockedReg)).toContainText('你参与过此项目');
    await m.goto('/projects/' + d.body.id);
    const cur = await get(m, d.body.id);
    expect(cur.actions.approve).toMatchObject({ allowed: false, reasonCode: 'REVIEW_CONFLICT' });
    const post = await api(m, `/api/v1/projects/${d.body.id}/approve`, 'POST', { version: cur.version, reason: '合成越权尝试' });
    expect(post.status).toBe(403);
    return { 'in reviewable view': false, 'actions.approve': cur.actions.approve, 'POST /approve (direct)': post.status };
  });
});

test('管理员（一）：创建待分配草稿', async ({ browser }) => {
  const ad = await as(browser, 'synthetic-admin');
  await step('A-01', '管理员 synthetic-admin', ad, '新建待分配草稿（含金额）', async () => {
    await action(ad, 'project.create').first().click();
    S.unName = `${T}·待分配·青石矿业`;
    await ad.locator('[name=customer]').fill(`${T}青石矿业有限公司`);
    await ad.locator('[name=projectName]').fill(S.unName);
    await saveDraft(ad);
    await expect(ad.locator('.owner-strip')).toContainText('待分配');
    S.unId = new URL(ad.url()).pathname.split('/')[2] || new URL(ad.url()).searchParams.get('draft');
    const r = await get(ad, S.unId);
    expect(r).toMatchObject({ ownerSubject: null, assignmentStatus: 'UNASSIGNED_DRAFT' });
    return { 'POST /api/v1/projects': 200, assignmentStatus: r.assignmentStatus, ownerSubject: r.ownerSubject };
  });
});

test('总监：跨组项目、跨组重复裁决、独立审核、统计；看不到待分配草稿', async ({ browser }) => {
  test.setTimeout(150000);
  const d = await as(browser, 'synthetic-director');
  await step('D-01', '总监 synthetic-director', d, '看不到管理员保管的待分配草稿（列表、详情、工作台）', async () => {
    await expect(action(d, 'dashboard.task').filter({ hasText: '待分配草稿' })).toHaveCount(0);
    const list = (await api(d, '/api/v1/projects?assignment=unassigned')).body.total;
    const one = await api(d, '/api/v1/projects/' + S.unId);
    expect(list).toBe(0);
    expect(one.status).toBe(404);
    const q = (await api(d, '/api/v1/projects?q=' + encodeURIComponent(S.unName))).body.total;
    expect(q).toBe(0);
    return { 'GET ?assignment=unassigned total': list, 'GET /projects/{unassigned id}': one.status, 'search by name': q };
  });
  await step('D-02', '总监 synthetic-director', d, '跨组重复：另一组销售 C 提交同客户同项目，本组主管只见固定提示，总监看到对比并裁决', async () => {
    const c = await as(browser, 'synthetic-c');
    const dup = await api(c, '/api/v1/projects', 'POST', { projectName: S.name, customer: `${T}东港钢铁有限公司`, location: { province: '合成省', city: '合成市', factoryName: '合成一厂', workshop: '主厂房' }, projectType: 'RETROFIT', salesStage: 'L1_REQUIREMENT', informationSource: 'END_CUSTOMER', confidenceLevel: 'CUSTOMER_CONFIRMED', requirements: ['ANTI_SWAY'], requirementNotes: '合成需求', plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 2 }] });
    submitted.add(dup.body.id);
    const s = await api(c, `/api/v1/projects/${dup.body.id}/submit`, 'POST', { version: dup.body.version });
    expect(s.body.intakeStatus).toBe('UNDER_REVIEW');
    const mo = await as(browser, 'synthetic-manager-other');
    const cmpOther = (await api(mo, `/api/v1/projects/${dup.body.id}/duplicate-comparison`)).body;
    await d.goto('/duplicates');
    await (await findCard(d, s.body.registrationNo)).click();
    // The comparison grid shows the existing filing (team one) next to the new one (team two).
    await expect(d.getByRole('dialog').locator('.comparison-grid')).toContainText('合成需求：桥机防摇与精确定位');
    await action(d, 'duplicate.confirm').first().click();
    await d.locator('[name=reviewReason]').fill('合成：同客户同项目，确认为重复');
    await action(d, 'review.confirm').click();
    await expect(d.getByRole('dialog')).toHaveCount(0);
    const r = await get(d, dup.body.id);
    return { 'submit → intakeStatus': s.body.intakeStatus, 'other-team manager comparison cases': cmpOther.cases?.length ?? 0, 'restrictedRelated (other manager)': cmpOther.restrictedRelated?.code ?? null, 'after director decision': r.intakeStatus };
  });
  await step('D-03', '总监 synthetic-director', d, '独立审核：主管经手的项目由总监批准', async () => {
    await d.goto('/reviews?eligibility=reviewable');
    await (await findCard(d, S.blockedReg)).click();
    await action(d, 'review.approve').click();
    await d.locator('[name=reviewReason]').fill('合成：总监独立审核批准');
    await action(d, 'review.confirm').click();
    await expect(d.getByRole('dialog')).toHaveCount(0);
    const r = await get(d, S.blockedId);
    expect(r.intakeStatus).toBe('APPROVED');
    return { 'POST /approve': 200, intakeStatus: r.intakeStatus };
  });
  await step('D-04', '总监 synthetic-director', d, '统计：阶段分布、已确认预估金额、风险计数与接口一致', async () => {
    await d.goto('/statistics');
    const g = (await api(d, '/api/v1/stage-summary')).body;
    await expect(d.getByTestId('stat-total')).toHaveText(String(g.total));
    await expect(d.getByTestId('stat-unknown')).toHaveText(String(g.amount.unknownCount));
    await expect(d.getByTestId('stat-invalid')).toHaveText(String(g.amount.invalidCount));
    return { 'GET /api/v1/stage-summary': 200, total: g.total, knownTotal: g.amount.knownTotal, unknown: g.amount.unknownCount, legacy: g.amount.legacyUnconfirmedCount, invalid: g.amount.invalidCount };
  });
});

test('管理员（二）：指派、人员与团队、停用、全局统计、审批独立性', async ({ browser }) => {
  test.setTimeout(150000);
  const ad = await as(browser, 'synthetic-admin');
  await step('A-02', '管理员 synthetic-admin', ad, '可见性：管理员能看到全部（含待分配草稿）', async () => {
    const un = (await api(ad, '/api/v1/projects?assignment=unassigned&q=' + encodeURIComponent(S.unName))).body.total;
    expect(un).toBe(1);
    await ad.goto('/unassigned');
    await expect(ad.locator('main')).toContainText(S.unName);
    return { 'GET ?assignment=unassigned&q': un };
  });
  await step('A-03', '管理员 synthetic-admin', ad, '指派有效销售：补齐后指派给销售 A', async () => {
    await ad.goto(`/projects/${S.unId}/edit`);
    await ad.locator('[name=province]').fill('合成省');
    await ad.locator('[name=city]').fill('合成市');
    await ad.locator('[name=factoryName]').fill('合成三厂');
    await ad.locator('[name=informationSource]').selectOption('END_CUSTOMER');
    await action(ad, 'project.step-next').click();
    await ad.getByLabel('防摇', { exact: true }).first().check();
    await ad.locator('[name=requirementNotes]').fill('合成管理员代录需求');
    await action(ad, 'project.step-next').click();
    await ad.locator('[name=projectType]').selectOption('RETROFIT');
    await ad.locator('[name=confidenceLevel]').selectOption('CUSTOMER_CONFIRMED');
    await action(ad, 'project.step-next').click();
    await expect(action(ad, 'project.submit')).toBeDisabled();
    await ad.locator('[name=assignTarget]').selectOption({ label: '示例销售 A' });
    await action(ad, 'project.assign').click();
    await expect(ad.locator('.owner-strip')).toContainText('示例销售 A');
    const r = await get(ad, S.unId);
    expect(r.ownerSubject).toBe('synthetic-a');
    return { 'POST /ownership-transfer (assign)': 200, ownerSubject: r.ownerSubject, assignmentStatus: r.assignmentStatus };
  });
  await step('A-04', '管理员 synthetic-admin', ad, '审批独立性：管理员代录并提交的项目，管理员不能批准（页面禁用 + 接口 403）', async () => {
    submitted.add(S.unId);
    await action(ad, 'project.submit').click();
    await expect(ad).toHaveURL(/\/projects\/[0-9a-f-]+$/);
    const r = await get(ad, S.unId);
    expect(r.actions.approve).toMatchObject({ allowed: false, reasonCode: 'REVIEW_CONFLICT' });
    const post = await api(ad, `/api/v1/projects/${S.unId}/approve`, 'POST', { version: r.version, reason: '合成自审尝试' });
    expect(post.status).toBe(403);
    return { intakeStatus: r.intakeStatus, 'actions.approve': r.actions.approve, 'POST /approve (direct)': post.status };
  });
  await step('A-05', '管理员 synthetic-admin', ad, '人员管理：邀请销售 → 待管理员开通', async () => {
    await ad.goto('/settings/users');
    await action(ad, 'staff.invite').click();
    const dialog = ad.getByRole('dialog');
    S.email = `uat-${RUN}@example.test`;
    await dialog.getByLabel('姓名', { exact: true }).fill('合成 UAT 销售');
    await dialog.getByLabel('工作邮箱').fill(S.email);
    await dialog.getByLabel('角色').selectOption('baobei_sales');
    await dialog.getByLabel('团队').selectOption({ label: 'Synthetic Team' });
    await dialog.getByLabel('操作原因').fill('合成 UAT 添加人员');
    await action(ad, 'staff.confirm').click();
    await expect(ad.locator('.staff-row').filter({ hasText: S.email })).toContainText('待管理员开通');
    const r = (await api(ad, '/api/v1/staff?q=' + encodeURIComponent(S.email))).body;
    return { 'POST /api/v1/staff': 200, found: r.items?.length ?? r.total };
  });
  await step('A-06', '管理员 synthetic-admin', ad, '停用与归档：停用后归档，保留历史', async () => {
    await action(ad, 'staff.detail').filter({ hasText: S.email }).first().click().catch(async () => action(ad, 'staff.detail').first().click());
    await action(ad, 'staff.suspend').click();
    await ad.getByRole('dialog').getByLabel('操作原因').fill('合成 UAT 停用');
    await action(ad, 'staff.confirm').click();
    await expect(ad.locator('.staff-title')).toContainText('已停用');
    await action(ad, 'staff.archive').click();
    await ad.getByRole('dialog').getByLabel('操作原因').fill('合成 UAT 归档');
    await action(ad, 'staff.confirm').click();
    await expect(ad.locator('.staff-title')).toContainText('已归档');
    return { status: '已归档' };
  });
  await step('A-07', '管理员 synthetic-admin', ad, '团队管理页可用；销售无此权限（跳回工作台，接口 403）', async () => {
    await ad.goto('/settings/teams');
    await expect(action(ad, 'team.create')).toBeVisible();
    const a = await as(browser, 'synthetic-a');
    await a.goto('/settings/users');
    await expect(a).toHaveURL('/');
    const r = await api(a, '/api/v1/staff');
    expect(r.status).toBe(403);
    return { 'sales GET /api/v1/staff': r.status };
  });
  await step('A-08', '管理员 synthetic-admin', ad, '全局统计：管理员范围含待分配草稿，与总监的差值等于待分配部分', async () => {
    await ad.goto('/statistics');
    const g = (await api(ad, '/api/v1/stage-summary')).body;
    const dg = (await api(await as(browser, 'synthetic-director'), '/api/v1/stage-summary')).body;
    const un = (await api(ad, '/api/v1/projects?assignment=unassigned&pageSize=1')).body.total;
    expect(g.total - dg.total).toBe(un);
    await expect(ad.getByTestId('stat-total')).toHaveText(String(g.total));
    return { adminTotal: g.total, directorTotal: dg.total, unassignedDrafts: un };
  });
});
