// R5-UX-R1 forensic measurement (no assertions): records the real canvas rect,
// projected region bounds, fill ratios and side-wall state, with screenshots.
// Run on the baseline (973e134) for BEFORE and on the fix for AFTER; output goes to
// $R5_OUT (default test output dir).
import { test } from '@playwright/test';
// Measurement tool, not a gate: run explicitly with R5_FORENSIC=1 (baseline and fix, same command).
test.skip(!process.env.R5_FORENSIC, 'R5 forensic measurement runs only when R5_FORENSIC=1');
import { mkdirSync, writeFileSync } from 'node:fs';
import { api, captureScenes, framing, located, locatedAssets, probe, settled } from './r5-geo-helpers';

const OUT = process.env.R5_OUT ?? '';
const tag = process.env.R5_TAG ?? 'run';
const sizes = (process.env.R5_SIZES ?? '1366x768,1653x1186,1920x1080,2560x1440').split(',');
const regions = (process.env.R5_REGIONS ?? '0,110000').split(',').map(Number);

for (const s of sizes)
  test(`R5 forensic ${tag} ${s}`, async ({ page }) => {
    const [w, h] = s.split('x').map(Number) as [number, number];
    await page.setViewportSize({ width: w, height: h });
    await captureScenes(page);
    await api(page, located, locatedAssets);
    const rows: unknown[] = [];
    for (const code of regions) {
      const path = code ? '/#/overview?region=' + regionPath(code) : '/#/overview';
      await page.goto(path);
      await settled(page, code || 100000);
      const f = await framing(page);
      const p = await probe(page);
      const file = `${tag}-${s}-${code || 'country'}.png`;
      const dir = OUT || test.info().outputDir;
      mkdirSync(dir, { recursive: true });
      await page.locator('[data-testid=spatial-stage]').screenshot({ path: `${dir}/${file}` });
      rows.push({ size: s, scope: code || 100000, ...f, walls: p.walls, countryRegionsVisible: p.countryRegionsVisible, gpu: { geometries: p.geometries, textures: p.textures, drawCalls: p.drawCalls }, screenshot: file });
    }
    const dir = OUT || test.info().outputDir;
    writeFileSync(`${dir}/${tag}-${s}.json`, JSON.stringify(rows, null, 1));
    console.log(JSON.stringify(rows.map((r) => { const x = r as { scope: number; fill: number; fillW: number; fillH: number; margins: unknown; walls: unknown; preset: string }; return [x.scope, x.preset, x.fillW, x.fillH, x.margins, x.walls]; })));
  });

function regionPath(code: number) {
  return String(code);
}
