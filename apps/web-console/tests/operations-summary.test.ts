// H2-1D-1: operations summary data layer — request identity, state machine, scope isolation.
import { describe, it, expect, vi } from 'vitest';
import { createSummaryController, summaryIdentity, summaryKey, type SummaryFetcher } from '../src/scene/operations-summary-provider';
import { ApiError, type AlarmWindow } from '../src/cloud/api';
import { summary } from './summary-fixtures';
import type { OperationsSummary } from '../../../contracts/typescript/types.gen';

type Pending = { region: number | null; window: AlarmWindow; signal: AbortSignal; resolve: (s: OperationsSummary) => void; reject: (e: unknown) => void };
/** Manually controlled fetcher: each call is held until the test resolves or rejects it. */
function manual() {
  const calls: Pending[] = [];
  const fetch: SummaryFetcher = (region, window, signal) =>
    new Promise((resolve, reject) => calls.push({ region, window, signal, resolve, reject }));
  return { calls, fetch };
}
const flush = () => new Promise((r) => setTimeout(r, 0));
const ZJ = 330000,
  GD = 440000;
const me = { identity: 'u1|P001:viewer', window: 'PT24H' as const };

describe('summary request identity', () => {
  it('key = identity + region (null ≡ 100000) + window; no identity → no key', () => {
    expect(summaryKey({ ...me, region: null })).toBe(summaryKey({ ...me, region: 100000 }));
    expect(summaryKey({ ...me, region: ZJ })).not.toBe(summaryKey({ ...me, region: GD }));
    expect(summaryKey({ ...me, region: ZJ })).not.toBe(summaryKey({ ...me, region: ZJ, window: 'P7D' }));
    expect(summaryKey({ ...me, region: ZJ })).not.toBe(summaryKey({ ...me, identity: 'u2|P001:viewer', region: ZJ }));
    expect(summaryKey({ identity: null, region: ZJ, window: 'PT24H' })).toBeNull();
  });
  it('identity changes with user AND with grants (revoked / added project or role)', () => {
    const s = (projects: [string, string][]) => ({
      userId: 'u1', displayName: '', csrfToken: '', expiresAt: '', idleExpiresAt: '',
      projects: projects.map(([projectId, role]) => ({ projectId, projectName: projectId, role: role as 'viewer' })),
    });
    const a = summaryIdentity(true, s([['P001', 'viewer'], ['P002', 'viewer']]));
    expect(summaryIdentity(true, s([['P002', 'viewer'], ['P001', 'viewer']]))).toBe(a); // order-free
    expect(summaryIdentity(true, s([['P001', 'viewer']]))).not.toBe(a); // grant revoked
    expect(summaryIdentity(true, s([['P001', 'operator'], ['P002', 'viewer']]))).not.toBe(a); // role changed
    expect(summaryIdentity(true, null)).toBeNull(); // logged out
    expect(summaryIdentity(false, null)).toBe('development-token');
  });
});

describe('summary state machine', () => {
  it('loading → ready; refresh keeps data as refreshing → ready', async () => {
    const m = manual();
    const c = createSummaryController({ fetch: m.fetch });
    c.setScope({ ...me, region: null });
    expect(c.snapshot.value.state).toBe('loading');
    expect(c.snapshot.value.data).toBeNull();
    m.calls[0]!.resolve(summary({ registered: 7 }));
    await flush();
    expect(c.snapshot.value).toMatchObject({ state: 'ready', error: '' });
    expect(c.snapshot.value.data!.metrics.assets.registered).toBe(7);
    expect(c.snapshot.value.fetchedAt).not.toBe('');
    void c.refresh();
    expect(c.snapshot.value.state).toBe('refreshing');
    expect(c.snapshot.value.data!.metrics.assets.registered).toBe(7);
    m.calls[1]!.resolve(summary({ registered: 8 }));
    await flush();
    expect(c.snapshot.value.data!.metrics.assets.registered).toBe(8);
  });

  it('first 503 → unavailable with no data (never 0); later success → ready', async () => {
    const m = manual();
    const c = createSummaryController({ fetch: m.fetch });
    c.setScope({ ...me, region: null });
    m.calls[0]!.reject(new ApiError('云端请求失败 (503)', 503));
    await flush();
    expect(c.snapshot.value).toMatchObject({ state: 'unavailable', data: null, status: 503 });
    void c.refresh();
    expect(c.snapshot.value.state).toBe('loading');
    m.calls[1]!.resolve(summary({ registered: 0, authorizedProjects: 0, projects: 0 }));
    await flush();
    expect(c.snapshot.value.state).toBe('ready'); // a legal 0 only after a successful response
    expect(c.snapshot.value.data!.metrics.assets.registered).toBe(0);
  });

  it('same-scope refresh failure → stale: keeps the last success of THIS scope and its fetch time', async () => {
    const m = manual();
    const c = createSummaryController({ fetch: m.fetch });
    c.setScope({ ...me, region: ZJ });
    m.calls[0]!.resolve(summary({ region: ZJ, registered: 21 }));
    await flush();
    const fetchedAt = c.snapshot.value.fetchedAt;
    void c.refresh();
    m.calls[1]!.reject(new ApiError('云端请求失败 (503)', 503));
    await flush();
    expect(c.snapshot.value).toMatchObject({ state: 'stale', fetchedAt, status: 503 });
    expect(c.snapshot.value.data!.metrics.assets.registered).toBe(21);
  });

  it('an invalid 200 body ({} / wrong scope) is an error, never zeros', async () => {
    const m = manual();
    const c = createSummaryController({ fetch: m.fetch });
    c.setScope({ ...me, region: ZJ });
    m.calls[0]!.resolve({} as OperationsSummary);
    await flush();
    expect(c.snapshot.value).toMatchObject({ state: 'unavailable', data: null });
    void c.refresh();
    m.calls[1]!.resolve(summary({ region: GD, registered: 9 })); // server answered another scope
    await flush();
    expect(c.snapshot.value).toMatchObject({ state: 'unavailable', data: null });
    expect(c.snapshot.value.error).toContain('不一致');
  });

  it('verifier rejection (administrative index mismatch) → unavailable, no numbers', async () => {
    const m = manual();
    const c = createSummaryController({ fetch: m.fetch, verify: async () => '行政区版本不一致' });
    c.setScope({ ...me, region: null });
    m.calls[0]!.resolve(summary({ registered: 5 }));
    await flush();
    expect(c.snapshot.value).toMatchObject({ state: 'unavailable', data: null, error: '行政区版本不一致' });
  });

  it('dedup: concurrent refreshes of the same scope start one request', async () => {
    const m = manual();
    const c = createSummaryController({ fetch: m.fetch });
    c.setScope({ ...me, region: null });
    void c.refresh();
    void c.refresh();
    c.setScope({ ...me, region: null }); // same scope again: no new request
    expect(m.calls).toHaveLength(1);
    expect(c.requests()).toBe(1);
  });
});

describe('P0 scope isolation', () => {
  it('Zhejiang → Guangdong → Zhejiang with out-of-order answers shows only the last scope', async () => {
    const m = manual();
    const c = createSummaryController({ fetch: m.fetch });
    c.setScope({ ...me, region: ZJ }); // #0
    c.setScope({ ...me, region: GD }); // #1 aborts #0
    c.setScope({ ...me, region: ZJ }); // #2 aborts #1
    expect(m.calls.map((x) => x.region)).toEqual([ZJ, GD, ZJ]);
    expect(m.calls[0]!.signal.aborted && m.calls[1]!.signal.aborted).toBe(true);
    // Answers arrive in reverse order, each claiming its own scope.
    m.calls[2]!.resolve(summary({ region: ZJ, registered: 21 }));
    await flush();
    m.calls[1]!.resolve(summary({ region: GD, registered: 99 }));
    m.calls[0]!.resolve(summary({ region: ZJ, registered: 11 })); // old Zhejiang answer
    await flush();
    expect(c.snapshot.value.state).toBe('ready');
    expect(c.snapshot.value.data!.scope.region).toBe(ZJ);
    expect(c.snapshot.value.data!.metrics.assets.registered).toBe(21);
  });

  it('a failing Guangdong never shows Zhejiang numbers as "stale"', async () => {
    const m = manual();
    const c = createSummaryController({ fetch: m.fetch });
    c.setScope({ ...me, region: ZJ });
    m.calls[0]!.resolve(summary({ region: ZJ, registered: 21 }));
    await flush();
    c.setScope({ ...me, region: GD });
    expect(c.snapshot.value).toMatchObject({ state: 'loading', data: null });
    m.calls[1]!.reject(new ApiError('云端请求失败 (503)', 503));
    await flush();
    expect(c.snapshot.value).toMatchObject({ state: 'unavailable', data: null });
    // Back to Zhejiang: its own cache shows at once (refreshing), never Guangdong's state.
    c.setScope({ ...me, region: ZJ });
    expect(c.snapshot.value.state).toBe('refreshing');
    expect(c.snapshot.value.data!.metrics.assets.registered).toBe(21);
  });

  it('alarm window is part of the scope: P7D does not reuse PT24H numbers', async () => {
    const m = manual();
    const c = createSummaryController({ fetch: m.fetch });
    c.setScope({ ...me, region: null });
    m.calls[0]!.resolve(summary({ recordsTotal: 150 }));
    await flush();
    c.setScope({ ...me, region: null, window: 'P7D' });
    expect(c.snapshot.value).toMatchObject({ state: 'loading', data: null });
  });

  it('logout / account switch / revoked grant drops every cached summary and in-flight request', async () => {
    const m = manual();
    const c = createSummaryController({ fetch: m.fetch });
    c.setScope({ ...me, region: null });
    m.calls[0]!.resolve(summary({ registered: 30 }));
    await flush();
    c.setScope({ identity: null, region: null, window: 'PT24H' }); // logged out (401 clears the session)
    expect(c.snapshot.value).toMatchObject({ state: 'idle', data: null });
    c.setScope({ identity: 'u2|P009:viewer', region: null, window: 'PT24H' }); // another account
    expect(c.snapshot.value).toMatchObject({ state: 'loading', data: null });
    m.calls[1]!.resolve(summary({ registered: 2 }));
    await flush();
    // Switching back to u1: the old cache is gone (no flash of u1's earlier numbers before a new answer).
    c.setScope({ ...me, region: null });
    expect(c.snapshot.value).toMatchObject({ state: 'loading', data: null });
    // A u2 answer arriving after the switch is dropped.
    const late = m.calls[2]!;
    c.setScope({ identity: 'u1|P001:viewer,P002:viewer', region: null, window: 'PT24H' }); // grant added
    late.resolve(summary({ registered: 30 }));
    await flush();
    expect(c.snapshot.value).toMatchObject({ state: 'loading', data: null });
  });

  it('abort() on page leave cancels the request; stale state only if this scope had data', async () => {
    const m = manual();
    const c = createSummaryController({ fetch: m.fetch });
    c.setScope({ ...me, region: null });
    c.abort();
    expect(m.calls[0]!.signal.aborted).toBe(true);
    m.calls[0]!.resolve(summary({ registered: 4 }));
    await flush();
    expect(c.snapshot.value.data).toBeNull();
  });

  it('60 s polling calls refresh; a hung request is not duplicated', async () => {
    vi.useFakeTimers();
    try {
      const m = manual();
      const { startSummaryPolling } = await import('../src/scene/operations-summary-provider');
      const c = createSummaryController({ fetch: m.fetch });
      c.setScope({ ...me, region: null });
      const stop = startSummaryPolling(c);
      vi.advanceTimersByTime(60_000);
      expect(m.calls).toHaveLength(1); // first request still pending → shared
      m.calls[0]!.resolve(summary({ registered: 1 }));
      await vi.runAllTicks();
      await Promise.resolve();
      await Promise.resolve();
      vi.advanceTimersByTime(60_000);
      expect(m.calls).toHaveLength(2);
      stop();
      vi.advanceTimersByTime(180_000);
      expect(m.calls).toHaveLength(2);
    } finally {
      vi.useRealTimers();
    }
  });
});
