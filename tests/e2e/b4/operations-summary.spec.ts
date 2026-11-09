import { test, expect } from '@playwright/test';
import { login } from './login';

// H2-1C: GET /api/v1/operations/summary on the production candidate image (real Keycloak login,
// real PostgreSQL, embedded administrative index). Read-only — no writes. Proves:
//   - the image carries a usable backend geo index (region drill resolves, version is a sha256);
//   - project scope comes from the real membership (b4-admin = P001 only, b4-p002 = P002 only, b4-noauth = none);
//   - 400 / 404 / 401 semantics on the real ingress; Cache-Control no-store.
async function summary(page: import('@playwright/test').Page, query = '') {
  return page.evaluate(async (query) => {
    const res = await fetch('/api/v1/operations/summary' + query, { credentials: 'same-origin' });
    return { status: res.status, cache: res.headers.get('cache-control'), body: await res.json().catch(() => null) };
  }, query);
}

test('运营统计接口：真实登录 + 授权范围 + 行政区索引 + 错误语义（只读）', async ({ page }) => {
  await login(page, 'b4-admin');
  const all = await summary(page);
  expect(all.status).toBe(200);
  expect(all.cache).toContain('no-store');
  expect(all.body.schemaVersion).toBe(1);
  expect(all.body.scope.region).toBeNull();
  expect(all.body.scope.authorizedProjects).toBe(1); // P001 only — P002 exists but is not granted
  expect(all.body.scope.geoIndexVersion).toMatch(/^[0-9a-f]{64}$/);
  expect(all.body.metrics.operation).toEqual({ status: 'NOT_AVAILABLE' });
  expect(all.body.metrics.pendingEvents).toEqual({ status: 'NOT_AVAILABLE' });
  expect(JSON.stringify(all.body)).not.toContain('P002');
  // The index is embedded in the image: a known province resolves to a named scope (200, possibly zero).
  const zj = await summary(page, '?region=330000&alarmWindow=P7D');
  expect(zj.status).toBe(200);
  expect(zj.body.scope.regionName).toBe('浙江省');
  expect(zj.body.freshness.alarmWindow).toBe('P7D');
  expect(zj.body.metrics.projects).toBeLessThanOrEqual(1);
  // Errors on the real ingress.
  expect((await summary(page, '?region=99')).status).toBe(400);
  expect((await summary(page, '?alarmWindow=P30D')).status).toBe(400);
  expect((await summary(page, '?project=P002')).status).toBe(400);
  expect((await summary(page, '?region=999999')).status).toBe(404);
});

test('运营统计接口：P002 用户与零授权用户只看到自己的范围', async ({ page, browser }) => {
  await login(page, 'b4-p002');
  const p2 = await summary(page);
  expect(p2.status).toBe(200);
  expect(p2.body.scope.authorizedProjects).toBe(1);
  const ctx = await browser.newContext();
  const none = await ctx.newPage();
  await login(none, 'b4-noauth');
  const empty = await summary(none);
  expect(empty.status).toBe(200);
  expect(empty.body.scope.authorizedProjects).toBe(0);
  expect(empty.body.metrics.assets.registered).toBe(0);
  expect(empty.body.metrics.alarm.records.total).toBe(0);
  expect(empty.body.regions).toEqual([]);
  await ctx.close();
  // Without a session the API is 401.
  const anon = await browser.newContext({ ignoreHTTPSErrors: true });
  const anonPage = await anon.newPage();
  await anonPage.goto('/');
  expect((await summary(anonPage)).status).toBe(401);
  await anon.close();
});
