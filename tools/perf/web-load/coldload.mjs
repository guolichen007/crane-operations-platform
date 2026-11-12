// R5-UX-PERF cold-load measurement (run with node, not a spec). Same build, same
// stub API, same throttled network; only the nginx config differs.
// usage: node r5-coldload.mjs <label> <baseURL> <out.json>
import { chromium } from '@playwright/test';
import { writeFileSync } from 'node:fs';

const [label, base, out] = process.argv.slice(2);
const NET = { offline: false, latency: 150, downloadThroughput: (1.6 * 1024 * 1024) / 8, uploadThroughput: (750 * 1024) / 8 };
const session = { userId: 'perf', displayName: 'perf', projects: [{ projectId: 'P001', projectName: 'Project-A', role: 'viewer' }], csrfToken: 'x'.repeat(32), expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' };
const kind = (url, type) => {
  const p = new URL(url).pathname;
  if (p.startsWith('/geo/')) return 'GeoJSON';
  if (p.startsWith('/api/') || p.startsWith('/auth/')) return 'API';
  if (/\.js$/.test(p)) return 'JS';
  if (/\.css$/.test(p)) return 'CSS';
  if (/\.(woff2?|ttf|otf)$/.test(p)) return 'Font';
  if (type === 'document' || p === '/' || p.endsWith('.html')) return 'HTML';
  return 'Other';
};
async function run(path, view) {
  const browser = await chromium.launch({ args: ['--enable-webgl', '--ignore-gpu-blocklist', '--enable-unsafe-swiftshader'] });
  const context = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  const page = await context.newPage();
  await page.route('**/auth/session', (r) => r.fulfill({ json: session }));
  await page.route('**/api/v1/**', (r) => r.fulfill({ json: [] }));
  const cdp = await context.newCDPSession(page);
  await cdp.send('Network.enable');
  await cdp.send('Network.setCacheDisabled', { cacheDisabled: true });
  await cdp.send('Network.emulateNetworkConditions', NET);
  const reqs = new Map();
  cdp.on('Network.responseReceived', (e) => reqs.set(e.requestId, { url: e.response.url, type: e.type.toLowerCase(), enc: e.response.headers['content-encoding'] ?? e.response.headers['Content-Encoding'] ?? '', status: e.response.status, fromCache: e.response.fromDiskCache, bytes: 0 }));
  cdp.on('Network.loadingFinished', (e) => { const r = reqs.get(e.requestId); if (r) r.bytes = e.encodedDataLength; });
  const t0 = Date.now();
  await page.goto(base + path, { waitUntil: 'load' });
  const loadMs = Date.now() - t0;
  // Ready = the stage rendered: 3D → 34 province labels; 2D → the SVG map paths.
  if (view === '3d') await page.locator('.province-summary').nth(33).waitFor({ timeout: 120000 });
  else await page.locator('.stage2d-map path').first().waitFor({ timeout: 120000 });
  const readyMs = Date.now() - t0;
  await page.waitForTimeout(500);
  const by = {};
  for (const r of reqs.values()) {
    const k = kind(r.url, r.type);
    by[k] ??= { requests: 0, bytes: 0, gzip: 0 };
    by[k].requests++;
    by[k].bytes += r.bytes;
    if (/gzip/.test(r.enc)) by[k].gzip++;
  }
  const total = Object.values(by).reduce((s, x) => s + x.bytes, 0);
  const three = [...reqs.values()].some((r) => /\/assets\/three-/.test(r.url));
  const charts = [...reqs.values()].some((r) => /\/assets\/charts-/.test(r.url));
  const mem = view === '3d' ? await page.evaluate(() => (window).__spatialDiagnostics?.()?.memory ?? null) : null;
  await browser.close();
  return { path, view, loadMs, readyMs, totalBytes: total, byKind: by, threeLoaded: three, chartsLoaded: charts, gpuMemory: mem };
}
const results = [];
for (const [path, view] of [['/#/overview', '3d'], ['/#/overview?view=2d', '2d'], ['/#/overview', '3d'], ['/#/overview?view=2d', '2d']]) results.push(await run(path, view));
writeFileSync(out, JSON.stringify({ label, network: NET, results }, null, 1));
console.log(label, JSON.stringify(results.map((r) => [r.view, r.readyMs, Math.round(r.totalBytes / 1024) + 'KiB', r.threeLoaded, r.chartsLoaded])));
