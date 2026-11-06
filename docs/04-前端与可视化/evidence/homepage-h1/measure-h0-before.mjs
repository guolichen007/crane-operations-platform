import { chromium } from '@playwright/test';
import { readFileSync, writeFileSync } from 'node:fs';
const example = JSON.parse(readFileSync('deploy/inventory/inventory.example.yaml', 'utf8'));
const session = { userId: 'acc', displayName: '验收员', projects: example.projects.map((p) => ({ projectId: p.id, projectName: p.name, role: 'viewer' })), csrfToken: 'x', expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' };
const projects = example.projects.map((p, i) => ({ ...p, adminCode: i === 0 ? 330481 : null, simulatedCount: 0, runtimeDeviceCount: 0, realConnectedCount: 0, assetDeviceCount: example.assetDevices.filter((a) => a.projectId === p.id).length }));
const assets = example.assetDevices.map((a) => ({ ...a, runtimeSource: null, runtimeDeviceId: null, connectivity: null }));
const b = await chromium.launch({ args: ['--enable-webgl', '--ignore-gpu-blocklist', '--enable-unsafe-swiftshader'] });
const out = [];
for (const [w, h] of [[1280, 720], [1366, 768], [1440, 900], [1920, 1080], [2560, 1440]]) {
  const page = await b.newPage({ viewport: { width: w, height: h }, deviceScaleFactor: 1 });
  await page.route('**/auth/session', (r) => r.fulfill({ json: session }));
  await page.route('**/api/v1/**', (r) => { const p = new URL(r.request().url()).pathname; r.fulfill({ json: p === '/api/v1/projects' ? projects : p === '/api/v1/assets/devices' ? assets : p.endsWith('/devices') || p.endsWith('/alarms') ? [] : {} }); });
  await page.goto('http://127.0.0.1:5185/#/overview', { waitUntil: 'commit', timeout: 90000 });
  await page.locator('.province-summary').nth(33).waitFor({ state: 'attached', timeout: 60000 });
  await page.locator('.scene-loading').waitFor({ state: 'hidden' }).catch(() => {});
  await page.waitForTimeout(1500);
  const m = await page.evaluate(() => {
    const stage = document.querySelector('.sandbox-stage').getBoundingClientRect();
    const vw = innerWidth, vh = innerHeight;
    const x0 = Math.max(0, stage.left), x1 = Math.min(vw, stage.right), y0 = Math.max(0, stage.top), y1 = Math.min(vh, stage.bottom);
    let area = Math.max(0, x1 - x0) * Math.max(0, y1 - y0);
    // Same overlay list as H1, plus H0's in-stage floating side panels and event strip if they sit over the stage.
    const sel = '.map-topline > *, .map-view-switch, .observation-switch, .scene-mode-dock, .map-tools, .map-attribution, .south-sea-inset-note, .left-context, .right-context, .event-strip';
    const seen = [];
    for (const el of document.querySelectorAll(sel)) {
      if (seen.some((s) => s.contains(el))) continue; seen.push(el);
      const r = el.getBoundingClientRect();
      area -= Math.max(0, Math.min(x1, r.right) - Math.max(x0, r.left)) * Math.max(0, Math.min(y1, r.bottom) - Math.max(y0, r.top));
    }
    return { netMapPct: +((area / (vw * vh)) * 100).toFixed(1), overflowX: document.documentElement.scrollWidth - vw, kpiCards: document.querySelectorAll('.kpi-card, .metric-card, .kpi-grid > *').length };
  });
  out.push({ viewport: `${w}x${h}`, sha: '76e1cef', ...m });
  await page.close();
}
await b.close();
writeFileSync('/tmp/h0-metrics.json', JSON.stringify(out, null, 1));
console.log(JSON.stringify(out));
