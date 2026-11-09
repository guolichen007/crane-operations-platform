// Authoritative homepage statistics (H2-1D-1). The single source of every API-mode KPI / Attention /
// region number is GET /api/v1/operations/summary (H2-1C). This module is deliberately separate from
// sceneProvider: sceneProvider feeds the MAP (Inventory + Shadow, spatial display only); this one feeds
// the NUMBERS. Neither computes the other's values.
//
// Request identity = (login identity, region, alarmWindow). Every response is cached and published
// only under the key it was requested for:
//   • a superseded request is aborted, and a late answer for an old key is dropped (sequence + key check),
//     so Zhejiang → Guangdong → Zhejiang can never show Guangdong numbers under Zhejiang or vice versa;
//   • a failed request may only fall back to an earlier success of the SAME key (→ "stale"); with no such
//     success the scope is "unavailable" — another region's numbers are never shown as "expired";
//   • a change of login identity (logout, account switch, grant change) drops the whole cache.
// 401 is handled by cloud/api.ts (clears the web session → identity null → cache dropped);
// 503 / network / invalid payload are "unavailable" or "stale", never 0.
import { shallowRef, type ShallowRef } from 'vue';
import type { OperationsSummary, WebSession } from '../../../../contracts/typescript/types.gen';
import { ApiError, type AlarmWindow } from '../cloud/api';

export type SummaryState = 'idle' | 'loading' | 'ready' | 'refreshing' | 'stale' | 'unavailable';
export type SummaryScope = { identity: string | null; region: number | null; window: AlarmWindow };
export type SummarySnapshot = {
  /** Key the snapshot belongs to (null = no identity, nothing requested). */
  key: string | null;
  state: SummaryState;
  /** Last successful summary OF THIS KEY (null while loading/unavailable). */
  data: OperationsSummary | null;
  /** Browser receive time of `data` (ISO), '' when none. */
  fetchedAt: string;
  /** Last error message for this key ('' when the last request succeeded). */
  error: string;
  /** HTTP status of the last error (0 = network/invalid payload), null when none. */
  status: number | null;
};
export type SummaryFetcher = (region: number | null, window: AlarmWindow, signal: AbortSignal) => Promise<OperationsSummary>;
/** Returns an error message when the summary must not be shown (e.g. administrative index mismatch). */
export type SummaryVerifier = (s: OperationsSummary) => Promise<string | null>;

export const REFRESH_MS = 60_000;
const MAX_ENTRIES = 24;

export function summaryKey(s: SummaryScope): string | null {
  return s.identity === null ? null : JSON.stringify([s.identity, s.region ?? 100000, s.window]);
}

/** Login identity for cache isolation: user + exact grants, so a revoked/added grant is a new identity. */
export function summaryIdentity(productionAuth: boolean, session: WebSession | null): string | null {
  if (!productionAuth) return 'development-token';
  if (!session) return null;
  return `${session.userId}|${session.projects
    .map((p) => `${p.projectId}:${p.role}`)
    .sort()
    .join(',')}`;
}

/** Minimal structural guard: a 200 that is not a summary (proxy page, `{}`) is an error, never zeros. */
function isSummary(v: unknown): v is OperationsSummary {
  const s = v as OperationsSummary;
  return (
    !!s &&
    s.schemaVersion === 1 &&
    typeof s.sampledAt === 'string' &&
    !!s.scope &&
    !!s.metrics?.assets &&
    !!s.metrics.real &&
    !!s.metrics.alarm?.records &&
    Array.isArray(s.regions)
  );
}

const idle: SummarySnapshot = { key: null, state: 'idle', data: null, fetchedAt: '', error: '', status: null };

export type SummaryController = {
  readonly snapshot: ShallowRef<SummarySnapshot>;
  setScope(scope: SummaryScope): void;
  /** Re-request the current key; concurrent calls for the same key share one request. */
  refresh(): Promise<void>;
  /** Abort the in-flight request (page leave); the cache is kept for the same identity. */
  abort(): void;
  /** Number of network requests started (diagnostics / tests). */
  readonly requests: () => number;
};

export function createSummaryController(deps: { fetch: SummaryFetcher; verify?: SummaryVerifier; now?: () => Date }): SummaryController {
  const now = deps.now ?? (() => new Date());
  const cache = new Map<string, { data: OperationsSummary; fetchedAt: string }>();
  const snapshot = shallowRef<SummarySnapshot>(idle);
  let scope: SummaryScope = { identity: null, region: null, window: 'PT24H' };
  let key: string | null = null;
  let seq = 0;
  let started = 0;
  let inflight: { key: string; seq: number; ctrl: AbortController; promise: Promise<void> } | null = null;

  function publish(state: SummaryState, extra: Partial<SummarySnapshot> = {}) {
    const entry = key ? cache.get(key) : undefined;
    snapshot.value = { key, state, data: entry?.data ?? null, fetchedAt: entry?.fetchedAt ?? '', error: '', status: null, ...extra };
  }
  function abort() {
    inflight?.ctrl.abort();
    inflight = null;
    seq++; // any answer still on the way is now superseded
  }
  function load(): Promise<void> {
    if (key === null) return Promise.resolve();
    if (inflight && inflight.key === key) return inflight.promise;
    abort();
    const myKey = key,
      mySeq = ++seq,
      ctrl = new AbortController(),
      { region, window } = scope;
    publish(cache.has(myKey) ? 'refreshing' : 'loading');
    started++;
    const promise = (async () => {
      try {
        const data = await deps.fetch(region, window, ctrl.signal);
        if (!isSummary(data)) throw new ApiError('运营统计响应格式无效', 0);
        // The answer must be for the scope that was asked for (defence in depth against mixing).
        if ((data.scope.region ?? null) !== region || data.freshness?.alarmWindow !== window)
          throw new ApiError('运营统计响应与请求范围不一致', 0);
        const problem = deps.verify ? await deps.verify(data) : null;
        if (mySeq !== seq || myKey !== key) return; // superseded: drop silently
        if (problem) throw new ApiError(problem, 0);
        cache.set(myKey, { data, fetchedAt: now().toISOString() });
        if (cache.size > MAX_ENTRIES) cache.delete(cache.keys().next().value!);
        publish('ready');
      } catch (e) {
        if (mySeq !== seq || myKey !== key) return;
        const status = e instanceof ApiError ? e.status : 0;
        const message = e instanceof Error && e.name !== 'AbortError' && e.name !== 'TimeoutError' ? e.message : '运营统计请求超时或被取消';
        publish(cache.has(myKey) ? 'stale' : 'unavailable', { error: message, status });
      } finally {
        if (inflight?.seq === mySeq) inflight = null;
      }
    })();
    inflight = { key: myKey, seq: mySeq, ctrl, promise };
    return promise;
  }
  return {
    snapshot,
    setScope(next) {
      if (next.identity !== scope.identity) cache.clear(); // logout / account switch / grant change
      const nextKey = summaryKey(next);
      const changed = nextKey !== key;
      scope = next;
      key = nextKey;
      if (!changed && snapshot.value.key === key) return;
      abort();
      if (key === null) {
        snapshot.value = idle;
        return;
      }
      void load();
    },
    refresh: load,
    abort() {
      const was = inflight;
      abort();
      if (was && key) publish(cache.has(key) ? 'stale' : 'unavailable', { error: '请求已取消', status: 0 });
    },
    requests: () => started,
  };
}

/** Periodic refresh (60 s) for the active page; never overlaps a running request (load() dedups). */
export function startSummaryPolling(c: SummaryController, every = REFRESH_MS): () => void {
  const timer = setInterval(() => void c.refresh(), every);
  return () => clearInterval(timer);
}
