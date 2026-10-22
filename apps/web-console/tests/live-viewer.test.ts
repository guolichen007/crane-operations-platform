import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { LiveViewer, viewerStorageKey, type ViewerSnapshot } from '../src/cloud/LiveViewer';
import { cloudDevice, viewerLease } from './cloud-fixtures';

class Socket {
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  close = vi.fn(() => this.onclose?.());
}
function setup(stale?: string) {
  const values = new Map(stale ? [[viewerStorageKey, stale]] : []);
  const storage = {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => {
      values.set(key, value);
    },
    removeItem: (key: string) => {
      values.delete(key);
    },
  };
  const target = new EventTarget(),
    sockets: Socket[] = [];
  let snapshot: ViewerSnapshot = { state: 'STOPPED', error: '' };
  const api = {
    openLive: vi.fn(async () => viewerLease()),
    renewLive: vi.fn(async () => viewerLease()),
    closeLive: vi.fn(async () => {}),
  };
  const onShadow = vi.fn();
  const viewer = new LiveViewer({
    storage,
    target,
    api,
    onShadow,
    onChange: (value) => {
      snapshot = value;
    },
    socket: () => {
      const ws = new Socket();
      sockets.push(ws);
      return ws as unknown as WebSocket;
    },
  });
  return { viewer, api, storage, target, sockets, onShadow, snapshot: () => snapshot };
}
beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime('2026-10-22T08:00:00Z');
});
afterEach(() => {
  vi.clearAllTimers();
  vi.useRealTimers();
});

it('stores a tab lease; reconnects with same viewer and clears errors after open', async () => {
  const h = setup();
  await h.viewer.start('SIM-001');
  expect(h.storage.getItem(viewerStorageKey)).toBe('viewer-1');
  expect(h.snapshot().state).toBe('CONNECTING');
  h.sockets[0]!.onopen?.();
  expect(h.snapshot().state).toBe('LIVE');
  h.sockets[0]!.onmessage?.({ data: JSON.stringify(cloudDevice()) });
  expect(h.onShadow).toHaveBeenCalledOnce();
  h.sockets[0]!.onclose?.();
  expect(h.snapshot().state).toBe('RECONNECTING');
  h.viewer.pollingAvailable();
  expect(h.snapshot().state).toBe('POLLING_FALLBACK');
  await vi.advanceTimersByTimeAsync(1000);
  expect(h.sockets).toHaveLength(2);
  h.sockets[1]!.onopen?.();
  expect(h.snapshot()).toMatchObject({ state: 'LIVE', error: '' });
  expect(h.api.openLive).toHaveBeenCalledOnce();
});
it('bounds retry backoff to 1/2/5/10/10 seconds and stops permanently on explicit stop', async () => {
  const h = setup();
  await h.viewer.start('SIM-001');
  for (const delay of [1000, 2000, 5000, 10000, 10000]) {
    h.sockets.at(-1)!.onclose?.();
    const count = h.sockets.length;
    await vi.advanceTimersByTimeAsync(delay - 1);
    expect(h.sockets).toHaveLength(count);
    await vi.advanceTimersByTimeAsync(1);
    expect(h.sockets).toHaveLength(count + 1);
  }
  h.sockets.at(-1)!.onclose?.();
  await h.viewer.stop();
  const count = h.sockets.length;
  expect(h.storage.getItem(viewerStorageKey)).toBeNull();
  await vi.advanceTimersByTimeAsync(400000);
  expect(h.sockets).toHaveLength(count);
  expect(h.api.renewLive).not.toHaveBeenCalled();
  expect(h.snapshot().state).toBe('STOPPED');
});
it('uses nonblocking keepalive DELETE on pagehide and avoids duplicate unload requests', async () => {
  const h = setup();
  await h.viewer.start('SIM-001');
  h.target.dispatchEvent(new Event('pagehide'));
  h.target.dispatchEvent(new Event('beforeunload'));
  expect(h.api.closeLive).toHaveBeenCalledExactlyOnceWith('viewer-1', true);
  expect(h.snapshot().state).toBe('STOPPED');
  await vi.advanceTimersByTimeAsync(400000);
  expect(h.sockets).toHaveLength(1);
});
it('cleans stale tab storage before another open and never auto-resumes LIVE on reload', async () => {
  const h = setup('old-viewer');
  await h.viewer.initialize();
  expect(h.api.closeLive).toHaveBeenCalledWith('old-viewer', false);
  expect(h.storage.getItem(viewerStorageKey)).toBeNull();
  expect(h.api.openLive).not.toHaveBeenCalled();
  expect(h.sockets).toHaveLength(0);
  expect(h.snapshot().state).toBe('STOPPED');
  await h.viewer.start('SIM-001');
  expect(h.api.closeLive.mock.invocationCallOrder[0]).toBeLessThan(
    h.api.openLive.mock.invocationCallOrder[0]!
  );
});
it('retains a failed unload cleanup id so a reload can retry; no lease is unbounded', async () => {
  const h = setup();
  await h.viewer.start('SIM-001');
  h.api.closeLive.mockRejectedValueOnce(Error('network down'));
  h.target.dispatchEvent(new Event('pagehide'));
  await vi.advanceTimersByTimeAsync(0);
  expect(h.storage.getItem(viewerStorageKey)).toBe('viewer-1');
  expect(h.snapshot().session).toBeUndefined();
  expect(viewerLease().ttlSeconds).toBe(300);
});
it('does not create a new lease when stale cleanup fails, then allows cleanup retry', async () => {
  const h = setup('old-viewer');
  h.api.closeLive.mockRejectedValueOnce(Error('offline'));
  await h.viewer.start('SIM-001');
  expect(h.api.openLive).not.toHaveBeenCalled();
  expect(h.storage.getItem(viewerStorageKey)).toBe('old-viewer');
  await h.viewer.start('SIM-001');
  expect(h.api.openLive).toHaveBeenCalledOnce();
});
for (const action of ['stop', 'dispose', 'pagehide'] as const)
  it(`closes a late open response after ${action}, without sockets or renewal`, async () => {
    const h = setup();
    let resolve!: (lease: ReturnType<typeof viewerLease>) => void;
    h.api.openLive.mockImplementationOnce(
      () =>
        new Promise((done) => {
          resolve = done;
        })
    );
    const opening = h.viewer.start('SIM-001');
    await vi.advanceTimersByTimeAsync(0);
    if (action === 'pagehide') h.target.dispatchEvent(new Event('pagehide'));
    else await h.viewer[action]();
    resolve(viewerLease());
    await opening;
    expect(h.api.closeLive).toHaveBeenCalledWith('viewer-1', true);
    expect(h.storage.getItem(viewerStorageKey)).toBeNull();
    expect(h.sockets).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(400000);
    expect(h.api.renewLive).not.toHaveBeenCalled();
  });
it('renews within TTL and ignores late renewal after stop', async () => {
  const h = setup();
  let resolve!: (lease: ReturnType<typeof viewerLease>) => void;
  h.api.renewLive.mockImplementationOnce(
    () =>
      new Promise((done) => {
        resolve = done;
      })
  );
  await h.viewer.start('SIM-001');
  await vi.advanceTimersByTimeAsync(60000);
  expect(h.api.renewLive).toHaveBeenCalledOnce();
  await h.viewer.stop();
  resolve(viewerLease());
  await vi.advanceTimersByTimeAsync(400000);
  expect(h.snapshot().session).toBeUndefined();
  expect(h.api.renewLive).toHaveBeenCalledOnce();
});
it('expires without reconnecting when renewal hangs, preserving server TTL fallback', async () => {
  const h = setup();
  h.api.renewLive.mockImplementation(() => new Promise(() => {}));
  await h.viewer.start('SIM-001');
  await vi.advanceTimersByTimeAsync(299999);
  h.sockets[0]!.onclose?.();
  await vi.advanceTimersByTimeAsync(2000);
  expect(h.snapshot()).toMatchObject({ state: 'STOPPED', session: undefined });
  expect(h.snapshot().error).toContain('到期');
  expect(h.sockets).toHaveLength(1);
});
it('stops on renewal failure and removes page listeners on dispose', async () => {
  const h = setup();
  h.api.renewLive.mockRejectedValueOnce(Error('401'));
  await h.viewer.start('SIM-001');
  await vi.advanceTimersByTimeAsync(60000);
  expect(h.snapshot().state).toBe('STOPPED');
  expect(h.snapshot().error).toContain('续租失败');
  await h.viewer.dispose();
  h.target.dispatchEvent(new Event('pagehide'));
  expect(h.api.closeLive).toHaveBeenCalledOnce();
});

it('a delayed failed-start cleanup cannot overwrite a newer successful session error state', async () => {
  const h = setup();
  let finishClose!: () => void;
  h.api.openLive.mockResolvedValueOnce({
    ...viewerLease(),
    sessionId: 'expired',
    expiresAt: '2020-01-01T00:00:00Z',
  });
  h.api.closeLive.mockImplementationOnce(
    () =>
      new Promise((done) => {
        finishClose = done;
      })
  );
  const oldStart = h.viewer.start('SIM-001');
  await vi.advanceTimersByTimeAsync(0);
  expect(h.snapshot().state).toBe('STOPPED');
  await h.viewer.start('SIM-001');
  h.sockets[0]!.onopen?.();
  finishClose();
  await oldStart;
  expect(h.snapshot()).toMatchObject({
    state: 'LIVE',
    error: '',
    session: { sessionId: 'viewer-1' },
  });
});
