import { type Page } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
import { test, expect, action, uniq, api } from './support/isolation';

// G4-S5-R2-P1: three everyday lifecycles closed in the page, against the real
// API and PostgreSQL: revoke an unfinished invitation, archive/restore a team,
// discard/restore a draft. Each loop also checks network failure, permission,
// a concurrent change, double clicks, archived records and an expired session.
// BAOBEI_EVIDENCE_DIR (optional) receives screenshots for the acceptance pack.
const shots = process.env.BAOBEI_EVIDENCE_DIR;
async function shot(page: Page, name: string) {
  if (shots) await page.screenshot({ path: `${shots}/${name}.png`, fullPage: true });
}
async function login(page: Page, subject: string) {
  await page.goto('/login');
  await page.locator('select').selectOption(subject);
  await action(page, 'auth.dev-login').click();
  await expect(page.getByRole('heading', { name: '工作台', exact: true })).toBeVisible();
}
const phoneFor = (seed: string, slot: number) => {
  let h = slot * 7919;
  for (const ch of seed) h = (h * 31 + ch.charCodeAt(0)) % 100_000_000;
  return '137' + String(h).padStart(8, '0');
};

test.describe.configure({ mode: 'serial' });

test('撤销未完成开户：确认、防重复、刷新保持、手机号释放后重新添加', async ({ page, browser }, info) => {
  test.skip(info.project.name !== 'desktop', 'one full loop per run');
  const unique = uniq('R2A');
  const phone = phoneFor(unique, 1);
  const name = '合成撤销' + unique.slice(-5);
  await login(page, 'synthetic-admin');
  await page.goto('/settings/users');
  await action(page, 'staff.invite').click();
  const add = page.getByRole('dialog', { name: '添加成员' });
  await add.getByLabel(/^姓名/).fill(name);
  await add.getByLabel(/^工作手机/).fill(phone);
  await add.getByLabel(/^团队/).selectOption({ index: 1 });
  await add.getByRole('button', { name: '添加成员' }).click();
  await expect(page.locator('.staff-title')).toContainText('待开通');
  const memberUrl = page.url();
  await shot(page, '01-admin-member-detail');

  // Open, cancel (nothing happens), reopen.
  await action(page, 'staff.revoke').click();
  let dlg = page.getByRole('dialog', { name: '撤销开户' });
  await expect(dlg).toContainText('工作手机和邮箱释放');
  await action(page, 'staff.cancel').click();
  await expect(dlg).toHaveCount(0);
  await expect(page.locator('.staff-title')).toContainText('待开通');
  await action(page, 'staff.revoke').click();
  dlg = page.getByRole('dialog', { name: '撤销开户' });
  // Required reason: shown next to the field, nothing sent.
  await dlg.getByRole('button', { name: '确认撤销开户' }).click();
  await expect(dlg.getByText('请填写操作原因').first()).toBeVisible();
  await dlg.getByLabel(/^操作原因/).fill('手机号填错，重新添加');
  // Network failure: error inside the dialog, input kept, retry succeeds.
  await page.route('**/revoke', (r) => r.abort('internetdisconnected'), { times: 1 });
  await dlg.getByRole('button', { name: '确认撤销开户' }).click();
  await expect(dlg.getByRole('alert')).toBeVisible();
  await expect(dlg.getByLabel(/^操作原因/)).toHaveValue('手机号填错，重新添加');
  await shot(page, '02-revoke-network-error');
  // Double click: one request wins, the second sees the new version or is ignored.
  const responses: number[] = [];
  page.on('response', (r) => { if (r.url().endsWith('/revoke')) responses.push(r.status()); });
  await dlg.getByRole('button', { name: '确认撤销开户' }).dblclick();
  await expect(page.locator('.staff-title')).toContainText('已撤销开户');
  expect(responses.filter((s) => s === 200)).toHaveLength(1);
  await expect(page.getByTestId('revoked-hint')).toBeVisible();
  await expect(page.getByText('请开通登录账号', { exact: true })).toHaveCount(0);
  await expect(action(page, 'staff.revoke')).toHaveCount(0);
  await expect(action(page, 'staff.edit')).toHaveCount(0);
  await expect(page.locator('.staff-audit')).toContainText('撤销开户');
  await shot(page, '03-revoked-detail');
  // Refresh keeps the state; back to the list; revoked filter finds it.
  await page.reload();
  await expect(page.locator('.staff-title')).toContainText('已撤销开户');
  await action(page, 'staff.back').click();
  await expect(page).toHaveURL(/\/settings\/users$/);
  await page.getByLabel('账号状态').selectOption('REVOKED');
  await expect(page.getByRole('row').filter({ hasText: name })).toBeVisible();
  await shot(page, '04-revoked-list');
  // The released phone can be used again immediately.
  await page.getByLabel('账号状态').selectOption('');
  await action(page, 'staff.invite').click();
  const again = page.getByRole('dialog', { name: '添加成员' });
  await again.getByLabel(/^姓名/).fill(name);
  await again.getByLabel(/^工作手机/).fill(phone);
  await again.getByLabel(/^团队/).selectOption({ index: 1 });
  await again.getByRole('button', { name: '添加成员' }).click();
  await expect(page.locator('.staff-title')).toContainText('待开通');
  expect(page.url()).not.toBe(memberUrl);

  // Concurrent change: another admin tab edits first → version conflict shown in the dialog.
  const other = await browser.newPage();
  await login(other, 'synthetic-admin');
  await action(page, 'staff.revoke').click();
  dlg = page.getByRole('dialog', { name: '撤销开户' });
  await dlg.getByLabel(/^操作原因/).fill('并发撤销');
  const id = page.url().split('/').pop()!.split('?')[0];
  const detail = await api(other, '/api/v1/staff/' + id);
  const m = (detail.body as { member: { version: number; name: string; phone: string; teamId: string; role: string } }).member;
  const edit = await api(other, '/api/v1/staff/' + id, 'PATCH', { name: m.name + '改', phone: m.phone, email: '', teamId: m.teamId, role: m.role, note: '', reason: '', version: m.version });
  expect(edit.status).toBe(200);
  await dlg.getByRole('button', { name: '确认撤销开户' }).click();
  await expect(dlg.getByRole('alert')).toContainText('刷新');
  await action(page, 'staff.cancel').click();
  // Session expired: the next action reports it instead of acting.
  await page.context().clearCookies();
  await page.reload();
  await expect(page).toHaveURL(/\/login/);
  await other.close();
});

test('团队归档与恢复：有成员时说明原因、归档后不可选、记录可查、恢复后可用', async ({ page, browser }, info) => {
  test.skip(info.project.name !== 'desktop', 'one full loop per run');
  const unique = uniq('R2B');
  const team = '合成团队' + unique.slice(-6);
  await login(page, 'synthetic-admin');
  await page.goto('/settings/teams');
  await action(page, 'team.create').click();
  let dlg = page.getByRole('dialog', { name: '新建团队' });
  await dlg.getByLabel(/^团队名称/).fill(team);
  await dlg.getByLabel(/^操作原因/).fill('新区域');
  await dlg.getByRole('button', { name: '保存团队' }).click();
  const card = page.locator(`[data-team="${team}"]`);
  await expect(card).toContainText('使用中');
  // Rename.
  await card.getByRole('button', { name: '编辑' + team }).click();
  dlg = page.getByRole('dialog', { name: '编辑团队' });
  await dlg.getByLabel(/^团队名称/).fill(team + '组');
  await dlg.getByLabel(/^操作原因/).fill('改名');
  await dlg.getByRole('button', { name: '保存团队' }).click();
  const renamed = team + '组';
  const card2 = page.locator(`[data-team="${renamed}"]`);
  await expect(card2).toContainText('成员 0 人');

  // A team with members cannot be archived: the button says why.
  const busy = page.locator('[data-team="Synthetic Team"]');
  await expect(busy.getByRole('button', { name: '归档Synthetic Team' })).toBeDisabled();
  await expect(busy).toContainText(/成员 \d+ 人/);
  await shot(page, '05-team-list');

  // Archive: cancel first, then network failure, then success.
  await card2.getByRole('button', { name: '归档' + renamed }).click();
  dlg = page.getByRole('dialog', { name: '归档团队' });
  await action(page, 'team.cancel').click();
  await expect(card2).toContainText('使用中');
  await card2.getByRole('button', { name: '归档' + renamed }).click();
  dlg = page.getByRole('dialog', { name: '归档团队' });
  await dlg.getByLabel(/^操作原因/).fill('区域合并');
  await page.route('**/archive', (r) => r.abort('internetdisconnected'), { times: 1 });
  await dlg.getByRole('button', { name: '确认归档' }).click();
  await expect(dlg.getByRole('alert')).toBeVisible();
  await expect(dlg.getByLabel(/^操作原因/)).toHaveValue('区域合并');
  await dlg.getByRole('button', { name: '确认归档' }).dblclick();
  const archivedCard = page.getByLabel('已归档团队').locator(`[data-team="${renamed}"]`);
  await expect(archivedCard).toContainText('已归档');
  await shot(page, '06-team-archived');
  await page.reload();
  await expect(page.getByLabel('已归档团队').locator(`[data-team="${renamed}"]`)).toBeVisible();

  // Not selectable for a new member.
  await page.goto('/settings/users');
  await action(page, 'staff.invite').click();
  const invite = page.getByRole('dialog', { name: '添加成员' });
  await expect(invite.getByLabel(/^团队/).locator('option', { hasText: renamed })).toHaveCount(0);
  await action(page, 'staff.cancel').click();

  // A manager cannot archive or restore (server refuses even a forged request).
  const mgr = await browser.newPage();
  await login(mgr, 'synthetic-manager');
  const list = await api(page, '/api/v1/teams');
  const t = (list.body as { items: { id: string; name: string; version: number }[] }).items.find((x) => x.name === renamed)!;
  expect((await api(mgr, `/api/v1/teams/${t.id}/restore`, 'POST', { version: t.version, reason: '越权' })).status).toBe(403);
  await mgr.close();

  // History and restore.
  await page.goto('/settings/teams');
  const ac = page.getByLabel('已归档团队').locator(`[data-team="${renamed}"]`);
  await ac.getByRole('button', { name: '查看' + renamed + '记录' }).click();
  const hist = page.getByRole('dialog', { name: '团队记录' });
  await expect(hist.getByLabel('团队记录')).toContainText('归档团队');
  await expect(hist.getByLabel('团队记录')).toContainText('修改团队');
  await shot(page, '07-team-history');
  await action(page, 'team.cancel').click();
  await ac.getByRole('button', { name: '恢复' + renamed }).click();
  dlg = page.getByRole('dialog', { name: '恢复团队' });
  await dlg.getByLabel(/^操作原因/).fill('重新启用');
  await dlg.getByRole('button', { name: '确认恢复' }).click();
  await expect(page.getByLabel('团队列表').locator(`[data-team="${renamed}"]`)).toContainText('使用中');
  // Stale version (another tab already restored) is reported, not applied.
  const stale = await api(page, `/api/v1/teams/${t.id}/archive`, 'POST', { version: t.version, reason: '旧版本' });
  expect(stale.status).toBe(409);
  await page.goto('/settings/users');
  await action(page, 'staff.invite').click();
  await expect(page.getByRole('dialog', { name: '添加成员' }).getByLabel(/^团队/).locator('option', { hasText: renamed })).toHaveCount(1);
  await shot(page, '08-team-restored-selectable');
});

test('草稿废弃与恢复：回收站、列表/管道/搜索不出现、恢复后内容不变并可继续编辑', async ({ page, browser }, info) => {
  test.skip(info.project.name !== 'desktop', 'one full loop per run');
  const unique = uniq('R2C');
  await login(page, 'synthetic-a');
  const created = await api(page, '/api/v1/projects', 'POST', {
    projectName: '合成废弃草稿' + unique, customer: '合成客户' + unique,
    location: { province: '合成省', city: '合成市', district: '', industrialPark: '', factoryName: '合成一厂', workshop: '', addressDetail: '' },
    salesStage: 'L1_REQUIREMENT', projectType: 'RETROFIT', informationSource: 'END_CUSTOMER', confidenceLevel: 'CUSTOMER_CONFIRMED',
    requirements: ['ANTI_SWAY'], requirementNotes: '保留的需求说明', plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 2 }], expectedPurchaseDate: '2027-01-01',
  });
  expect(created.status).toBe(200);
  const p = created.body as { id: string; version: number };
  // Enter from the list, filtered by the unique name.
  await page.goto('/projects?q=' + encodeURIComponent(unique));
  await page.getByText('合成废弃草稿' + unique).first().click();
  await action(page, 'project.detail').click();
  await expect(page).toHaveURL(new RegExp('/projects/' + p.id));
  await expect(action(page, 'project.discard')).toBeVisible();

  // Another sales cannot see or discard it.
  const b = await browser.newPage();
  await login(b, 'synthetic-b');
  expect((await api(b, `/api/v1/projects/${p.id}/discard`, 'POST', { version: p.version, reason: 'x' })).status).toBe(404);
  await b.close();

  await action(page, 'project.discard').click();
  let dlg = page.getByRole('dialog', { name: '废弃草稿' });
  await expect(dlg).toContainText('从未提交的草稿');
  await dlg.getByRole('button', { name: '确认' }).click();
  await expect(dlg.getByText('请填写操作原因').first()).toBeVisible();
  await dlg.getByLabel(/^操作原因/).fill('客户取消需求');
  await page.route('**/discard', (r) => r.abort('internetdisconnected'), { times: 1 });
  await dlg.getByRole('button', { name: '确认' }).click();
  await expect(dlg.getByRole('alert')).toBeVisible();
  await expect(dlg.getByLabel(/^操作原因/)).toHaveValue('客户取消需求');
  await dlg.getByRole('button', { name: '确认' }).dblclick();
  await expect(page.getByTestId('discarded-note')).toBeVisible();
  await expect(page.locator('.detail-heading .badge').first()).toHaveText('已废弃（回收站）');
  await expect(action(page, 'project.edit')).toHaveCount(0);
  await shot(page, '09-draft-discarded');
  await page.reload();
  await expect(page.getByTestId('discarded-note')).toBeVisible();

  // Gone from list, search and pipeline; present in the recycle bin.
  await page.goto('/projects?q=' + encodeURIComponent(unique));
  await expect(page.getByText('没有符合筛选条件的项目')).toBeVisible();
  await page.goto('/pipeline?q=' + encodeURIComponent(unique));
  await expect(page.getByText('合成废弃草稿' + unique)).toHaveCount(0);
  await page.goto('/recycle-bin');
  await expect(page.getByRole('heading', { name: /草稿回收站/ })).toBeVisible();
  await page.getByText('合成废弃草稿' + unique).first().click();
  await shot(page, '10-recycle-bin');
  await action(page, 'project.detail').click();
  // Back path returns to the bin.
  await expect(page.getByRole('button', { name: '返回草稿回收站' })).toBeVisible();

  // Restore: optional reason; content unchanged; editable again.
  await action(page, 'project.restore').click();
  dlg = page.getByRole('dialog', { name: '恢复草稿' });
  await dlg.getByRole('button', { name: '确认' }).click();
  await expect(page.getByTestId('discarded-note')).toHaveCount(0);
  await expect(page.locator('.badge').first()).toContainText('草稿');
  await expect(page.locator('.detail-grid')).toContainText('合成客户' + unique);
  await shot(page, '11-draft-restored');
  await action(page, 'project.edit').click();
  await expect(page).toHaveURL(new RegExp('/projects/' + p.id + '/edit'));
  await page.goBack();
  await page.getByRole('button', { name: '返回草稿回收站' }).click();
  await expect(page).toHaveURL(/\/recycle-bin/);
  await expect(page.getByRole('heading', { name: /草稿回收站/ })).toBeVisible();
  await expect(page.getByText('合成废弃草稿' + unique)).toHaveCount(0);
  // The pipeline shows it again; list ↔ pipeline round trip keeps the filter.
  await page.goto('/projects?q=' + encodeURIComponent(unique));
  await expect(page.getByText('合成废弃草稿' + unique).first()).toBeVisible();
  await action(page, 'view.pipeline').click();
  await expect(page).toHaveURL(/\/pipeline\?.*q=/);
  await shot(page, '12-list-pipeline-return');
  // Stale discard (old version) is refused.
  expect((await api(page, `/api/v1/projects/${p.id}/discard`, 'POST', { version: p.version, reason: 'x' })).status).toBe(409);
  // Tidy: put it back in the bin so other specs' lists are unaffected.
  const cur = (await api(page, '/api/v1/projects/' + p.id)).body as { version: number };
  expect((await api(page, `/api/v1/projects/${p.id}/discard`, 'POST', { version: cur.version, reason: '测试结束' })).status).toBe(200);
});

test('撤销待核对在管理员页面显示原因与核对入口（模拟服务端 REVIEW 响应）', async ({ page }, info) => {
  test.skip(info.project.name !== 'desktop', 'UI rendering check');
  const unique = uniq('R2F1');
  await login(page, 'synthetic-admin');
  await page.goto('/settings/users');
  await action(page, 'staff.invite').click();
  const add = page.getByRole('dialog', { name: '添加成员' });
  await add.getByLabel(/^姓名/).fill('合成待核对' + unique.slice(-5));
  await add.getByLabel(/^工作手机/).fill(phoneFor(unique, 3));
  await add.getByLabel(/^团队/).selectOption({ index: 1 });
  await add.getByRole('button', { name: '添加成员' }).click();
  await expect(page.locator('.staff-title')).toContainText('待开通');
  // Real member, with the detail response rewritten to the REVIEW state the
  // server produces when the Keycloak delete fails (Go tests cover the real path).
  await page.route(/\/api\/v1\/staff\/[0-9a-f-]{36}$/, async (route) => {
    const res = await route.fetch();
    const body = await res.json();
    body.member.accountState = 'REVIEW';
    body.member.identityLinked = true;
    body.member.accountOperation = { id: 'x', operation: 'REVOKE', state: 'REVIEW', step: 'KC_DELETE', detail: 'REVOKE_DELETE_FAILED', at: new Date().toISOString() };
    await route.fulfill({ response: res, json: body });
  });
  await page.reload();
  await expect(page.getByTestId('account-operation')).toContainText('撤销开户待核对');
  await expect(page.getByTestId('account-operation')).toContainText('手机号暂不释放');
  await expect(action(page, 'staff.account-reconcile')).toBeVisible();
  await expect(action(page, 'staff.revoke')).toHaveCount(0);
  await shot(page, '13-revoke-review');
});

test('G5-D1 服务端字段错误就近显示并聚焦：废弃原因、团队名称', async ({ page, browser }, info) => {
  test.skip(info.project.name !== 'desktop', 'one check per run');
  const unique = uniq('G5D1');
  await login(page, 'synthetic-a');
  const created = await api(page, '/api/v1/projects', 'POST', {
    projectName: '合成字段错误' + unique, customer: '合成客户', location: { province: '合成省', city: '合成市', district: '', industrialPark: '', factoryName: '合成一厂', workshop: '', addressDetail: '' },
    salesStage: 'L1_REQUIREMENT', projectType: 'RETROFIT', informationSource: 'END_CUSTOMER', confidenceLevel: 'CUSTOMER_CONFIRMED', requirements: [], plannedEquipment: [],
  });
  const p = created.body as { id: string; version: number };
  // Real server: a blank reason sent directly is refused with details.field.
  const blank = await api(page, `/api/v1/projects/${p.id}/discard`, 'POST', { version: p.version, reason: '   ' });
  expect(blank.status).toBe(400);
  expect(blank.body.details.field).toBe('reason');
  await page.goto('/projects/' + p.id);
  await action(page, 'project.discard').click();
  const dlg = page.getByRole('dialog', { name: '废弃草稿' });
  await dlg.getByLabel(/^操作原因/).fill('有内容但服务端拒绝');
  // Forward the real refusal body the server gives for a blank reason.
  await page.route('**/discard', (r) => r.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify(blank.body) }), { times: 1 });
  await dlg.getByRole('button', { name: '确认' }).click();
  await expect(dlg.locator('.field-error')).toHaveText('请填写废弃原因');
  await expect(dlg.getByLabel(/^操作原因/)).toBeFocused();
  await expect(dlg.getByLabel(/^操作原因/)).toHaveValue('有内容但服务端拒绝');
  await shot(page, '14-reason-field-error');
  await action(page, 'dialog.cancel').click();
  const admin = await browser.newPage();
  await login(admin, 'synthetic-admin');
  await admin.goto('/settings/teams');
  await action(admin, 'team.create').click();
  const t = admin.getByRole('dialog', { name: '新建团队' });
  await t.getByLabel(/^团队名称/).fill('Synthetic Team');
  await t.getByLabel(/^操作原因/).fill('重名');
  await t.getByRole('button', { name: '保存团队' }).click();
  await expect(t.locator('.field-error')).toContainText('同名团队');
  await expect(t.getByLabel(/^团队名称/)).toBeFocused();
  await admin.close();
});
