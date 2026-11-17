import { test, expect, action, api, uniq } from './support/isolation';
import type { Page } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';

// V1.1 R6 (AC-21, AC-22): technical brief added after filing, versions,
// conflict, clarification questions; the filed equipment stays as filed.
async function login(p: Page, subject: string) {
  await p.goto('/login');
  await p.locator('select').selectOption(subject);
  await action(p, 'auth.dev-login').click();
  await expect(p.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
}

test('V1.1 R6 技术需求：报备后补录、版本冲突、澄清问题、原始报备不变', async ({ page, browser, baseURL }) => {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await login(page, 'synthetic-a');
  const name = uniq('V11技术');
  const created = await api(page, '/api/v1/projects', 'POST', { projectName: name, customer: name + '客户', salesStage: 'L2_TECHNICAL', plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 2 }], version: 1 });
  expect(created.status).toBe(200);
  const before = created.body.documentHash;
  await page.goto('/projects/' + created.body.id);
  await action(page, 'project.tab').filter({ hasText: '设备与技术需求' }).click();
  await expect(page.locator('.technical')).toContainText('正式报备时不要求全部填写');
  await action(page, 'technical.edit').click();
  await page.locator('[name=scenario]').fill('炼钢车间铸造起重机自动化');
  await page.locator('[name=siteConstraints]').fill('高温，检修窗口每月一次');
  await action(page, 'technical.row-add').first().click();
  await page.getByLabel('参数名称 1').fill('定位精度');
  await page.getByLabel('参数数值 1').fill('±5');
  await page.getByLabel('参数单位 1').fill('mm');
  // Another session saves first: this save is a conflict, input kept.
  const other = await api(page, `/api/v1/projects/${created.body.id}/technical-brief`, 'PUT', { version: 0, status: 'COLLECTING', scenario: '另一会话' });
  expect(other.status).toBe(200);
  await action(page, 'technical.save').click();
  await expect(page.getByRole('dialog')).toContainText('已被其他人更新');
  await expect(page.locator('[name=scenario]')).toHaveValue('炼钢车间铸造起重机自动化');
  // Retry with the reloaded version.
  await page.getByRole('dialog').getByRole('button', { name: '取消' }).click();
  await action(page, 'technical.edit').click();
  await page.locator('[name=scenario]').fill('炼钢车间铸造起重机自动化');
  await action(page, 'technical.save').click();
  await expect(page.locator('.technical-head')).toContainText('版本 2');
  await expect(page.locator('.technical')).toContainText('BRIDGE×2');
  // Clarification: ask and answer.
  await action(page, 'technical.ask').click();
  await page.locator('[name=techQuestion]').fill('夜间是否允许施工？');
  await action(page, 'technical.ask-confirm').click();
  await expect(page.locator('.question-list')).toContainText('待答复');
  await action(page, 'technical.answer').click();
  await page.locator('[name=techAnswer]').fill('允许 22:00 后');
  await action(page, 'technical.answer-confirm').click();
  await expect(page.locator('.question-list')).toContainText('已答复');
  // The filed document is unchanged.
  const after = (await api(page, '/api/v1/projects/' + created.body.id)).body;
  expect(after.documentHash).toBe(before);
  expect(after.plannedEquipment).toEqual([expect.objectContaining({ equipmentType: 'BRIDGE', quantity: 2 })]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  // Another team cannot read it.
  const ctx = await browser.newContext({ baseURL });
  const c = await ctx.newPage();
  await login(c, 'synthetic-c');
  expect((await api(c, `/api/v1/projects/${created.body.id}/technical-brief`)).status).toBe(404);
  await ctx.close();
  expect(errors).toEqual([]);
});
