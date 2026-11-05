import { test, expect, type Page, type Locator } from '@playwright/test';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { login, sessionCsrf } from './login';
import { code, assertWriteTarget, RUN_ID } from './run';

// G4-Q: real-browser visual regression of the asset business pages at
// 1280×720 / 1366×768 / 1920×1080 against the isolated Keycloak + PostgreSQL
// fixture. Every scene asserts: no horizontal overflow, and its key action is
// visible and not covered by another layer (hit-test at the element centre).
// Raw PNGs are named <sha>-<scene>-<w>x<h>.png and an index (JSON + Markdown)
// is written next to them for the evidence artifact.

const SHA = (process.env.GITHUB_SHA || process.env.B4_VISUAL_SHA || 'local').slice(0, 12);
const OUT = join('test-results', 'b4-visual');
const VIEWPORTS = [
  [1280, 720],
  [1366, 768],
  [1920, 1080],
] as const;
type Shot = { scene: string; viewport: string; file: string; keyAction: string; overflowPx: number };
const shots: Shot[] = [];

async function overflowPx(page: Page): Promise<number> {
  return page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
}

/** Visible, inside the viewport, and the topmost element at its centre is itself (or a descendant). */
async function expectUnobstructed(page: Page, target: Locator, label: string): Promise<void> {
  await expect(target, `${label} 可见`).toBeVisible();
  await target.scrollIntoViewIfNeeded();
  const box = await target.boundingBox();
  expect(box, `${label} 有布局`).not.toBeNull();
  const vp = page.viewportSize()!;
  const cx = box!.x + box!.width / 2;
  const cy = box!.y + box!.height / 2;
  expect(cx >= 0 && cx <= vp.width && cy >= 0 && cy <= vp.height, `${label} 中心在视口内`).toBe(true);
  const handle = await target.elementHandle();
  const hit = await page.evaluate(
    ({ el, x, y }) => {
      const top = document.elementFromPoint(x, y);
      return !!top && (top === el || el!.contains(top));
    },
    { el: handle, x: cx, y: cy },
  );
  expect(hit, `${label} 未被浮层遮挡`).toBe(true);
}

async function capture(page: Page, scene: string, keyAction: Locator, label: string) {
  await expectUnobstructed(page, keyAction, label);
  const overflow = await overflowPx(page);
  expect(overflow, `${scene} 无横向溢出`).toBeLessThanOrEqual(0);
  const vp = page.viewportSize()!;
  const viewport = `${vp.width}x${vp.height}`;
  const file = `${SHA}-${scene}-${viewport}.png`;
  await page.screenshot({ path: join(OUT, file), animations: 'disabled', mask: [page.locator('.command-actions time')] });
  shots.push({ scene, viewport, file, keyAction: label, overflowPx: overflow });
}

async function createAsset(page: Page, label: string): Promise<{ id: string; version: number }> {
  const csrf = await sessionCsrf(page);
  return page.evaluate(async ({ csrf, assetCode }) => {
    const res = await fetch('/api/v1/assets', {
      method: 'POST',
      headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' },
      body: JSON.stringify({ projectId: 'P001', assetType: 'CRANE', assetCode, displayName: '视觉回归资产' }),
    });
    return (await res.json()) as { id: string; version: number };
  }, { csrf, assetCode: code('CRN', label) });
}

for (const [w, h] of VIEWPORTS) {
  test(`${w}×${h} 资产业务页面视觉与可操作性`, async ({ page }) => {
    mkdirSync(OUT, { recursive: true });
    await page.setViewportSize({ width: w, height: h });
    await login(page, 'b4-admin');
    await assertWriteTarget(page);
    const created = await createAsset(page, `VIS${w}`);

    // 资产台账
    await page.goto('/#/assets?project=P001');
    await expect(page.getByTestId('asset-table')).toBeVisible();
    await capture(page, 'asset-ledger', page.getByTestId('asset-create'), '新建资产');

    // 资产详情
    await page.goto(`/#/assets/${created.id}`);
    await expect(page.getByTestId('asset-detail')).toContainText('视觉回归资产');
    await capture(page, 'asset-detail', page.getByRole('button', { name: '编辑', exact: true }), '编辑');

    // 409 对比：打开编辑、他人更新、保存得到冲突对比
    await page.getByRole('button', { name: '编辑', exact: true }).click();
    const form = page.getByTestId('asset-edit-form');
    await form.getByLabel('资产名称').fill('我的草稿');
    const csrf = await sessionCsrf(page);
    await page.evaluate(async ({ id, csrf, version }) => {
      await fetch(`/api/v1/assets/${id}`, {
        method: 'PATCH',
        headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json', 'If-Match': `"${version}"` },
        body: JSON.stringify({ displayName: '他人的修改' }),
      });
    }, { id: created.id, csrf, version: created.version });
    await form.getByRole('button', { name: '保存' }).click();
    await expect(page.getByTestId('asset-conflict-diff')).toBeVisible();
    await capture(page, 'conflict-409', page.getByTestId('asset-rebase'), '基于最新版本重新编辑');

    // DirtyGuard：有改动时 Esc 触发确认，取消后表单仍在
    let prompted = 0;
    page.once('dialog', async (d) => {
      prompted += 1;
      await d.dismiss();
    });
    await page.keyboard.press('Escape');
    await expect.poll(() => prompted).toBe(1);
    await expect(form).toBeVisible();
    await capture(page, 'dirtyguard-kept', form.getByRole('button', { name: '取消' }), '取消（草稿保留）');
    page.once('dialog', (d) => d.accept());
    await form.getByRole('button', { name: '取消' }).click();
    await expect(form).toHaveCount(0);

    // 设备安装
    await page.locator('.tabs').getByRole('button', { name: '设备', exact: true }).click();
    await page.getByRole('button', { name: '安装 Equipment' }).click();
    const eqForm = page.getByTestId('equipment-create-form');
    await expect(eqForm.getByLabel('设备类型选择').locator('option', { hasText: '电机（MOTOR）' })).toHaveCount(1);
    await capture(page, 'equipment-install', eqForm.getByRole('button', { name: '安装' }), '安装');
    await eqForm.getByRole('button', { name: '取消' }).click();

    // 位置中心
    await page.goto('/#/locations?project=P001');
    await expect(page.getByTestId('location-center')).toBeVisible();
    await capture(page, 'location-center', page.getByTestId('location-center').locator('h1'), '位置中心标题');

    // 设备目录
    await page.goto('/#/catalog');
    await expect(page.getByTestId('catalog-center')).toBeVisible();
    await capture(page, 'equipment-catalog', page.getByTestId('catalog-center').locator('h1'), '设备目录标题');
  });
}

test.afterAll(() => {
  if (!shots.length) return;
  mkdirSync(OUT, { recursive: true });
  const index = { sha: SHA, runId: RUN_ID, generatedAt: new Date().toISOString(), browser: 'Playwright Chromium (CI isolated, SwiftShader — not a GPU measurement)', shots };
  writeFileSync(join(OUT, `${SHA}-index.json`), JSON.stringify(index, null, 2));
  const rows = shots.map((s) => `| ${s.scene} | ${s.viewport} | ${s.keyAction} | ${s.overflowPx} | ${s.file} |`).join('\n');
  writeFileSync(
    join(OUT, `${SHA}-index.md`),
    `# B4 视觉证据索引 ${SHA}\n\nRUN_ID ${RUN_ID}；真实浏览器运行生成，非本地伪造；不代表 GPU 性能。\n\n| 场景 | 分辨率 | 关键操作（可见且未遮挡） | 横向溢出(px) | 文件 |\n|---|---|---|---|---|\n${rows}\n`,
  );
});
