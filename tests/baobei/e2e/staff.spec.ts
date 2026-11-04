import { test, expect, type Page } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';

const action = (page: Page, id: string) => page.locator('[data-action-id="' + id + '"]:visible');
test.use({ actionTimeout: 10000 });

async function login(page: Page, subject: string) {
  await page.goto('/login');
  await page.locator('select').selectOption(subject);
  await action(page, 'auth.dev-login').click();
  await expect(page.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
}

async function confirm(page: Page, reason: string) {
  await page.getByRole('dialog').getByLabel('操作原因').fill(reason);
  await action(page, 'staff.confirm').click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
}

async function findMember(page: Page, query: string) {
  await page.getByLabel('姓名或邮箱').fill(query);
  await action(page, 'staff.search').click();
  await expect(page.locator('.staff-list')).toContainText(query);
  await action(page, 'staff.detail').click();
}

test('B3 主管邀请、调整权限、审核、停用与交接', async ({ page, browser, baseURL }, info) => {
  const mobile = info.project.name === 'mobile-390';
  const sales = mobile ? 'synthetic-staff-mobile' : 'synthetic-staff-desktop';
  const context = await browser.newContext({ baseURL });
  const salesPage = await context.newPage();
  const unique = info.project.name + '-' + Date.now();
  const email = 'invite-' + unique + '@example.test';
  const projectName = 'SyntheticB3-' + unique;
  try {
    await login(salesPage, sales);
    const project = await salesPage.evaluate(async (projectName) => {
      const me = await (await fetch('/api/v1/me')).json();
      const post = async (path: string, body: unknown) => {
        const response = await fetch(path, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': me.csrfToken },
          body: JSON.stringify(body),
        });
        if (!response.ok) throw new Error('Synthetic project setup failed ' + response.status);
        return response.json();
      };
      const draft = await post('/api/v1/projects', {
        projectName, customer: projectName,
        location: { province: '合成省', city: '合成市', factoryName: '合成一厂', workshop: '合成车间' },
        projectType: 'RETROFIT', salesStage: 'L1_REQUIREMENT', informationSource: 'END_CUSTOMER',
        confidenceLevel: 'CUSTOMER_CONFIRMED', requirements: ['ANTI_SWAY'],
        requirementNotes: 'Synthetic 需求', plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 2 }],
      });
      return post('/api/v1/projects/' + draft.id + '/submit', { version: draft.version });
    }, projectName);

    await login(page, 'synthetic-manager');
    await page.getByRole('button', { name: '报备审核', exact: true }).click();
    await action(page, 'review.open').filter({ hasText: projectName }).click();
    await action(page, 'review.approve').click();
    await page.locator('[name=reviewReason]').fill('Synthetic B3 审核');
    await action(page, 'review.confirm').click();
    await expect(page.getByRole('dialog')).toHaveCount(0);
    await page.getByRole('button', { name: '团队成员', exact: true }).click();
    const invite = action(page, 'staff.invite');
    await invite.focus();
    await invite.press('Enter');
    const dialog = page.getByRole('dialog', { name: '添加团队成员' });
    await expect(dialog.getByLabel('团队')).toBeDisabled();
    await expect(dialog.getByLabel('角色')).toBeDisabled();
    await page.keyboard.press('Escape');
    await expect(invite).toBeFocused();
    await invite.click();
    await dialog.getByLabel('姓名', { exact: true }).fill('合成待开通销售');
    await dialog.getByLabel('工作邮箱').fill(email);
    await confirm(page, 'Synthetic B3 添加人员');
    await expect(page.locator('.staff-row').filter({ hasText: email })).toContainText('待管理员开通');
    await page.getByLabel('账号状态').selectOption('PENDING_PROVISION');
    await expect(page.locator('.staff-row')).toHaveCount(1);
    await page.getByLabel('排序').selectOption('lastLogin');
    await expect(action(page, 'staff.previous')).toBeDisabled();
    await expect(action(page, 'staff.next')).toBeDisabled();
    await action(page, 'staff.detail').click();
    await expect(page.getByText('等待管理员处理受控开通回执', { exact: false })).toBeVisible();
    await action(page, 'staff.edit').click();
    await page.getByRole('dialog').getByLabel('备注').fill('Synthetic B3 团队备注');
    await confirm(page, 'Synthetic B3 编辑资料');
    await expect(page.getByText('Synthetic B3 团队备注')).toBeVisible();
    await action(page, 'staff.permissions').click();
    await action(page, 'staff.cancel').click();
    await expect(action(page, 'staff.permissions')).toBeFocused();
    await action(page, 'staff.permissions').click();
    await page.getByRole('dialog').getByLabel('记录跟进').uncheck();
    await confirm(page, 'Synthetic B3 收回跟进权限');
    await expect(page.getByText('记录跟进 · 关闭', { exact: true })).toBeVisible();
    await expect(page.getByText('Synthetic B3 收回跟进权限')).toBeVisible();

    await action(page, 'staff.back').click();
    await findMember(page, sales + '@example.test');
    await action(page, 'staff.suspend').click();
    await confirm(page, 'Synthetic B3 离职停用');
    await expect(page.locator('.staff-title')).toContainText('已停用');
    expect((await salesPage.request.get('/api/v1/me')).status()).toBe(401);
    await page.getByRole('checkbox', { name: project.registrationNo, exact: false }).check();
    await action(page, 'staff.transfer').click();
    const handoff = page.getByRole('dialog', { name: '批量移交项目' });
    await handoff.getByLabel('查找接收销售').fill('synthetic-b@example.test');
    await action(page, 'staff.target-search').click();
    await handoff.getByRole('combobox', { name: '接收销售', exact: true }).selectOption({ label: '示例销售 B · Synthetic Team' });
    const transferReason = 'Synthetic B3 离职交接 ' + unique;
    await confirm(page, transferReason);
    await expect(page.getByRole('list', { name: '移交结果' })).toContainText('已移交');
    await action(page, 'staff.reactivate').click();
    await confirm(page, 'Synthetic B3 恢复申请');
    await expect(page.locator('.staff-title')).toContainText('待管理员开通');
    await expect(page.getByText(transferReason, { exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);
  } finally {
    await context.close();
  }
});

test('B3 管理员维护团队与邀请高级角色', async ({ page }, info) => {
  const unique = info.project.name + '-' + Date.now();
  const team = '合成团队-' + unique;
  const email = 'lead-' + unique + '@example.test';
  await login(page, 'synthetic-admin');
  await page.getByRole('button', { name: '团队管理', exact: true }).click();
  await action(page, 'team.create').click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('团队名称').fill(team);
  await dialog.getByLabel('操作原因').fill('Synthetic B3 建立团队');
  await action(page, 'team.confirm').click();
  const edit = page.getByRole('button', { name: '编辑' + team, exact: true });
  await edit.click();
  await action(page, 'team.cancel').click();
  await expect(edit).toBeFocused();
  await edit.click();
  await dialog.getByLabel('团队名称').fill(team + '-更新');
  await dialog.getByLabel('操作原因').fill('Synthetic B3 调整团队名称');
  await action(page, 'team.confirm').click();
  await expect(page.getByRole('heading', { name: team + '-更新' })).toBeVisible();

  await page.getByRole('button', { name: '人员管理', exact: true }).click();
  await action(page, 'staff.invite').click();
  await dialog.getByLabel('姓名', { exact: true }).fill('合成新主管');
  await dialog.getByLabel('工作邮箱').fill(email);
  await dialog.getByLabel('角色').selectOption('baobei_manager');
  await dialog.getByLabel('团队').selectOption({ label: team + '-更新' });
  await confirm(page, 'Synthetic B3 高级角色审批');
  await expect(page.locator('.staff-row').filter({ hasText: email })).toContainText('待管理员开通');
  await action(page, 'staff.detail').click();
  await expect(page.getByText('人员管理仅展示项目计数。')).toBeVisible();
  await action(page, 'staff.suspend').click();
  await confirm(page, 'Synthetic B3 停用高级角色');
  await action(page, 'staff.archive').click();
  await confirm(page, 'Synthetic B3 保留历史并归档');
  await expect(page.locator('.staff-title')).toContainText('已归档');
  expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);
});
