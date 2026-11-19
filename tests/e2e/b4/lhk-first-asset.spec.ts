import { test, expect } from '@playwright/test';
import { login } from './login';

// R9 B7 on the REAL stack (Keycloak 26.8.1, production BFF, PostgreSQL, Chromium): the first real
// asset LHK-CRANE-001 imported by the fixture with the production onboarding code, its SIMULATION
// runtime fed with the reviewed fixture batches (lhk-normalized-simulation-v1) and one test-only
// threshold rule at 100 %. The maintainer views 计量 / 寿命 / 预警 / 工单 / 历史 in the browser, turns
// the alert into a work order, completes it with a (simulated) replacement and sees the next cycle.
// verify-lhk-state then checks PostgreSQL (REAL untouched, ledger intact, cycle 2). Real machine
// control stays off; nothing is REAL.
test('@r9-lhk 真实浏览器：老河口首台资产 计量 → 寿命预警 → 工单 → 更换 → 下一周期 → 历史', async ({ page }) => {
  test.setTimeout(240_000);
  await login(page, 'b4-maintainer');
  await page.goto('/#/assets/LHK-CRANE-001');
  const d = page.getByTestId('asset-detail');
  await expect(d).toContainText('老河口液压抓斗起重机');

  // 台账与点位: 55 original points, raw PLC address text, pending nameplate facts, review items.
  await d.getByTestId('tab-ledger').click();
  await expect(d.locator('[data-point]')).toHaveCount(55);
  await expect(d.locator('[data-point="LHK-P001"]')).toContainText('DB505.DBX32.0');
  await expect(d.locator('[data-point="LHK-P021"]')).toContainText('地址待核（无法解析）');
  await expect(d.getByTestId('r9-pending')).toContainText('额定起重量（t）');
  await expect(d.getByTestId('r9-pending')).toContainText('待确认');
  await expect(d.getByTestId('r9-issues')).toContainText('重复 PLC 地址组');
  await page.screenshot({ path: 'test-results/b4-visual/r9-lhk-ledger.png' });

  // 运行监测: simulated meters, loudly marked, no REAL data.
  await d.getByTestId('tab-monitor').click();
  await expect(d.getByTestId('r9-sim-banner')).toBeVisible();
  await expect(d.getByTestId('r9-no-real')).toBeVisible();
  const p001 = d.locator('[data-meter="LHK-P001.cycles"][data-mode="SIMULATION"]');
  await expect(p001).toContainText('模拟数据');
  await expect(p001).toContainText('1,000,000');
  await expect(d.locator('[data-mode="REAL"]')).toHaveCount(0);
  await page.screenshot({ path: 'test-results/b4-visual/r9-lhk-monitor.png' });

  // 寿命与维护: the test-only rule is DUE (100 %), the PCT_100 alert → work order → assign → start
  // → complete with a replacement → cycle 2, 0 %.
  await d.getByTestId('tab-maintenance').click();
  const rule = d.locator('[data-rule="T-LHK-CONTACTOR"]');
  await expect(rule).toContainText('模拟测试规则');
  await expect(rule.locator('[data-status]')).toHaveText(/到期|逾期/);
  const alert = d.locator('[data-stage="PCT_100"][data-mode="SIMULATION"]');
  await expect(alert).toContainText('使用量 100%');
  await alert.getByTestId('r9-create-wo').click();
  await expect(d.getByTestId('r9-notice')).toContainText('已生成工单 SIM-WO');
  await d.getByTestId('r9-wo-open').click();
  const dlg = page.getByTestId('r9-wo-dialog');
  await expect(dlg).toContainText('模拟数据');
  await dlg.getByTestId('r9-wo-assignee').fill('维修班 B4');
  await dlg.getByTestId('r9-wo-assign').click();
  await expect(dlg.getByTestId('r9-wo-start')).toBeVisible();
  await dlg.getByTestId('r9-wo-start').click();
  await dlg.getByTestId('r9-wo-basis').fill('更换大车总接触器（模拟验收）');
  await dlg.getByTestId('r9-wo-replace').check();
  await dlg.getByTestId('r9-wo-reason').fill('达到模拟寿命阈值');
  await dlg.getByTestId('r9-wo-complete').click();
  await expect(d.getByTestId('r9-notice')).toContainText('已完成');
  await expect(rule).toContainText('第 2 周期');
  await expect(rule).toContainText('使用量 0%');
  await expect(d.locator('[data-wo-status="COMPLETED"]')).toBeVisible();
  await page.screenshot({ path: 'test-results/b4-visual/r9-lhk-maintenance.png', fullPage: true });

  // 历史: work order transitions and the simulated replacement, ledger revision of the import.
  await d.getByTestId('tab-history').click();
  await expect(d.locator('[data-category="REPLACEMENT"]').first()).toContainText('模拟数据');
  await expect(d.locator('[data-category="WORK_ORDER"]').first()).toBeVisible();
  await expect(d.locator('[data-category="FAULT"]').first()).toContainText('SIM_TEST_FAULT');

  // 维护与到期中心: REAL by default (the simulated row is hidden), visible when switched.
  await page.goto('/#/maintenance');
  const c = page.getByTestId('maintenance-center');
  await expect(c.getByTestId('mc-rows')).toContainText('法定定期检验');
  await expect(c.locator('[data-mode="SIMULATION"]')).toHaveCount(0);
  await c.getByTestId('mc-mode').selectOption('ALL');
  await expect(c.locator('[data-mode="SIMULATION"]').first()).toBeVisible();
  await page.screenshot({ path: 'test-results/b4-visual/r9-lhk-center.png', fullPage: true });
});

test('@r9-lhk 只读与越权：viewer 可看不可处理，无授权账号看不到老河口资产', async ({ browser }) => {
  const viewer = await (await browser.newContext()).newPage();
  await login(viewer, 'b4-viewer');
  await viewer.goto('/#/assets/LHK-CRANE-001');
  await viewer.getByTestId('tab-maintenance').click();
  await expect(viewer.getByTestId('r9-rules')).toBeVisible();
  await expect(viewer.getByTestId('r9-create-wo')).toHaveCount(0);
  await expect(viewer.getByTestId('r9-wo-open')).toHaveCount(0);
  const outsider = await (await browser.newContext()).newPage();
  await login(outsider, 'b4-p002');
  const res = await outsider.evaluate(async () => (await fetch('/api/v1/assets/LHK-CRANE-001/meters')).status);
  expect(res).toBe(404);
});
