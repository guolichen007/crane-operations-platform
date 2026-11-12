// R5-UX-PERF warm-cache measurement: cold visit, then a second visit in the same persistent profile.
// usage: node tools/perf/web-load/warm-cache.mjs <label> <baseURL> <out.json>  (needs stub-cloud-api.py on :18081 behind nginx)
import { chromium } from '@playwright/test';
import { writeFileSync } from 'node:fs';
const [label, base, out] = process.argv.slice(2);
const session = { userId: 'perf', displayName: 'perf', projects: [{ projectId: 'P001', projectName: 'Project-A', role: 'viewer' }], csrfToken: 'x'.repeat(32), expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z' };
// Persistent profile: Chromium's HTTP disk cache is not used by default ephemeral contexts in headless shell.
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
const ctx = await chromium.launchPersistentContext(mkdtempSync(join(tmpdir(), 'r5warm-')), { viewport: { width: 1920, height: 1080 } });
const browser = { close: () => ctx.close() };
const context = ctx;
const page = ctx.pages()[0] ?? (await ctx.newPage());
const cdp = await context.newCDPSession(page);
await cdp.send('Network.enable');
if (!process.env.NO_THROTTLE) await cdp.send('Network.emulateNetworkConditions', { offline: false, latency: 150, downloadThroughput: 204800, uploadThroughput: 96000 });
const visit = async () => {
  const reqs = new Map();
  const on = (e) => reqs.set(e.requestId, { url: e.response.url, status: e.response.status, cache: e.response.fromDiskCache || e.response.fromPrefetchCache || false, bytes: 0 });
  const fin = (e) => { const r = reqs.get(e.requestId); if (r) r.bytes = e.encodedDataLength; };
  const extra = (e) => { const r = reqs.get(e.requestId); if (r && e.statusCode === 304) r.status = 304; };
  const mem = (e) => { const r = reqs.get(e.requestId); if (r) r.cache = 'memory'; };
  cdp.on('Network.responseReceived', on); cdp.on('Network.loadingFinished', fin); cdp.on('Network.requestServedFromCache', mem);
  const t0 = Date.now();
  await page.goto(base + '/#/overview?view=2d');
  await page.locator('.stage2d-map path').first().waitFor({ timeout: 120000 });
  const ms = Date.now() - t0;
  await page.waitForLoadState('networkidle');
  await page.waitForTimeout(1500);
  cdp.off('Network.responseReceived', on); cdp.off('Network.loadingFinished', fin); cdp.off('Network.requestServedFromCache', mem);
  const v = [...reqs.values()].filter((r) => !/\/(api|auth)\//.test(r.url));
  return { ms, bytes: v.reduce((s, r) => s + r.bytes, 0), requests: v.length, cached: v.filter((r) => r.cache).length, notModified: v.filter((r) => r.status === 304).length, hashedRefetched: v.filter((r) => /\/assets\//.test(r.url) && !r.cache).map((r) => new URL(r.url).pathname) };
};
const cold = await visit();
await page.goto('about:blank');
const warm = await visit();
await browser.close();
writeFileSync(out, JSON.stringify({ label, cold, warm }, null, 1));
console.log(label, JSON.stringify({ cold: [cold.ms, cold.bytes], warm: [warm.ms, warm.bytes, 'cached', warm.cached, '304', warm.notModified, 'assetsRefetched', warm.hashedRefetched.length] }));
