import { chromium } from 'playwright'; // run from repo root: node docs/04-前端与可视化/prototype/homepage-h1/measure.mjs <outdir> (serve this folder on :8099)
const out = process.argv[2];
const browser = await chromium.launch({ args: ['--no-sandbox'] });
const res = {};
for (const [w, h] of [[1280, 720], [1366, 768], [1920, 1080]]) {
  for (const mode of ['3d', '2d']) {
    const page = await browser.newPage({ viewport: { width: w, height: h } });
    await page.goto(`http://127.0.0.1:8099/index.html${mode === '2d' ? '#2d' : ''}`);
    await page.waitForTimeout(800);
    const m = await page.evaluate(() => {
      const s = document.querySelector('.stage').getBoundingClientRect();
      const vw = innerWidth, vh = innerHeight;
      // stage ∩ viewport
      const x0 = Math.max(0, s.left), x1 = Math.min(vw, s.right), y0 = Math.max(0, s.top), y1 = Math.min(vh, s.bottom);
      let area = Math.max(0, x1 - x0) * Math.max(0, y1 - y0);
      // minus occluding overlays inside the stage (tools, dock, crumb, legal)
      for (const el of document.querySelectorAll('.stage .tools,.stage .dock,.stage .crumb,.stage .legal')) {
        const r = el.getBoundingClientRect();
        const ix = Math.max(0, Math.min(x1, r.right) - Math.max(x0, r.left)), iy = Math.max(0, Math.min(y1, r.bottom) - Math.max(y0, r.top));
        area -= ix * iy;
      }
      let minFont = 99; for (const e of document.querySelectorAll('main *')) { if ([...e.childNodes].some(n => n.nodeType === 3 && n.textContent.trim()) && e.getBoundingClientRect().width) minFont = Math.min(minFont, parseFloat(getComputedStyle(e).fontSize)); }
      return { stage: { x: Math.round(s.left), y: Math.round(s.top), w: Math.round(s.width), h: Math.round(s.height) }, netMapPct: +(area / (vw * vh) * 100).toFixed(1), scrollW: document.documentElement.scrollWidth, minFont };
    });
    res[`${w}x${h}-${mode}`] = m;
    await page.screenshot({ path: `${out}/proto-${w}x${h}-${mode}.png` });
    await page.close();
  }
}
console.log(JSON.stringify(res, null, 1));
await browser.close();
