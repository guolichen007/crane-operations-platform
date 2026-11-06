// H1-R1-5 first-content measurement (2D vs 3D). Usage, from the repo root:
//   npm run build && (cd apps/web-console && npx vite preview --port 4173 --strictPort) &
//   node "docs/04-前端与可视化/evidence/homepage-h1-r1/measure-first-content.mjs"
// Demo data mode (no API). Timings under SwiftShader are for 2D/3D comparison only, not GPU performance.
import { chromium } from 'playwright';
const base = 'http://127.0.0.1:4173';
const browser = await chromium.launch({ args: ['--enable-webgl', '--ignore-gpu-blocklist', '--enable-unsafe-swiftshader'] });
const out = [];
for (const view of ['2d', '3d']) for (let i = 0; i < 5; i++) {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await ctx.newPage();
  const bytes = { js: 0, three: 0, geo: 0 };
  page.on('response', async (r) => { try { const u = r.url(); const b = (await r.body()).length; if (u.endsWith('.js')) { bytes.js += b; if (/three-|MapSceneManager|SpatialStage3D/.test(u)) bytes.three += b; } if (u.includes('/geo/')) bytes.geo += b; } catch {} });
  const t0 = Date.now();
  await page.goto(`${base}/#/overview${view === '2d' ? '?view=2d' : ''}`);
  await page.locator('[data-kpi=devices] .home-kpi-value').filter({ hasText: /\d/ }).waitFor();
  const kpiMs = Date.now() - t0;
  if (view === '2d') await page.locator('.stage2d-map path').first().waitFor();
  else { await page.locator('.province-summary').nth(33).waitFor({ timeout: 60000 }); await page.locator('.scene-loading').waitFor({ state: 'hidden' }); }
  const mapMs = Date.now() - t0;
  await page.waitForTimeout(500);
  out.push({ view, run: i + 1, kpiMs, mapMs, jsKB: Math.round(bytes.js / 1024), threeKB: Math.round(bytes.three / 1024), geoKB: Math.round(bytes.geo / 1024) });
  await ctx.close();
}
await browser.close();
console.log(JSON.stringify(out));
