import { type Page } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
import { test, expect, action, api, uniq } from './support/isolation';
// G3-4-A1 UAT findings, fixed in this batch:
//  F1  after "放弃修改" restores the saved amount, the save status no longer
//      says "数值有误，未保存".
//  F2  the detail page offers only the sales stages the server accepts (the
//      same rule as the pipeline); before, every stage was listed and an
//      illegal choice ended in a generic 409 toast.
async function login(p: Page, subject: string) {
  await p.goto('/login');
  await p.locator('select').selectOption(subject);
  await action(p, 'auth.dev-login').click();
  await expect(p.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
}
const doc = (name: string, stage: string) => ({
  projectName: name, customer: name + '客户', location: { province: '合成省', city: '合成市', factoryName: name, workshop: '一车间' },
  projectType: 'RETROFIT', salesStage: stage, informationSource: 'END_CUSTOMER', confidenceLevel: 'CUSTOMER_CONFIRMED',
  requirements: ['ANTI_SWAY'], requirementNotes: '合成需求', plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 2 }],
  estimatedAmount: 125000.25, knowledge: { estimatedAmount: 'KNOWN' },
});

test('F1 放弃无效输入后保存状态恢复，不再显示“数值有误”', async ({ page }, info) => {
  await login(page, 'synthetic-a');
  const r = await api(page, '/api/v1/projects', 'POST', doc(uniq('A1-F1-' + info.project.name), 'L1_REQUIREMENT'));
  await page.goto(`/projects/${r.body.id}/edit`);
  await action(page, 'project.step-jump').filter({ hasText: '商务' }).first().click();
  const status = page.locator('.page-heading').first();
  await page.locator('[name=estimatedAmount]').fill('1.234');
  await expect(status).toContainText('数值有误，未保存');
  await action(page, 'number.revert-input').click();
  await expect(page.locator('[name=estimatedAmount]')).toHaveValue('125000.25');
  await expect(status).not.toContainText('数值有误');
  await expect(action(page, 'project.save-draft')).toBeEnabled();
});

test('F2 详情页“修改销售阶段”只列服务端允许的阶段', async ({ page, cleanup }, info) => {
  await login(page, 'synthetic-a');
  // Draft: any other stage is allowed (editable).
  const d = await api(page, '/api/v1/projects', 'POST', doc(uniq('A1-F2草稿-' + info.project.name), 'L1_REQUIREMENT'));
  await page.goto('/projects/' + d.body.id);
  await action(page, 'project.stage-change').click();
  await expect(page.locator('select[name=targetStage] option')).toHaveCount(12); // 请选择 + 11 others
  await action(page, 'dialog.cancel').click();
  // Submitted: only the next stage, HOLD and LOST.
  cleanup.track(d.body.id);
  const s = await api(page, `/api/v1/projects/${d.body.id}/submit`, 'POST', { version: d.body.version });
  expect(s.status).toBe(200);
  await page.reload();
  await action(page, 'project.stage-change').click();
  expect(await page.locator('select[name=targetStage] option').allTextContents()).toEqual(['请选择', '技术交流', '暂缓', '已失单']);
  await page.locator('[name=targetStage]').selectOption('L2_TECHNICAL');
  await page.locator('[name=governanceReason]').fill('合成：进入技术交流');
  await action(page, 'governance.confirm').click();
  await expect(page.locator('.detail-heading')).toContainText('技术交流');
  // The server rule is unchanged: a skipped stage is still refused.
  const cur = (await api(page, '/api/v1/projects/' + d.body.id)).body;
  expect((await api(page, `/api/v1/projects/${d.body.id}/stage-change`, 'POST', { version: cur.version, salesStage: 'L6_TENDER', reason: '合成跳级' })).status).toBe(409);
});

test('V1 窄屏：拒绝切换步骤后，非法字段滚动到可见区域且获得焦点', async ({ browser }, info) => {
  test.skip(info.project.name !== 'desktop', 'sets its own 390 viewport');
  const p = await (await browser.newContext({ baseURL: 'http://127.0.0.1:5188', viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })).newPage();
  await login(p, 'synthetic-a');
  const r = await api(p, '/api/v1/projects', 'POST', doc(uniq('A1-V1'), 'L1_REQUIREMENT'));
  await p.goto(`/projects/${r.body.id}/edit`);
  await action(p, 'project.step-jump').filter({ hasText: '商务' }).first().click();
  const field = p.locator('[name=estimatedAmount]');
  await field.fill('1.234');
  await p.evaluate(() => scrollTo(0, 0));
  await action(p, 'project.step-next').click();
  await expect(field).toBeFocused();
  // Visible and not under the fixed action bar.
  const box = (await field.boundingBox())!;
  const bar = (await p.locator('.wizard-actions').boundingBox())!;
  expect(box.y).toBeGreaterThanOrEqual(0);
  expect(box.y + box.height).toBeLessThanOrEqual(bar.y);
  await p.context().close();
});
