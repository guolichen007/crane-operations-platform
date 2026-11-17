import { afterEach, describe, expect, it, vi } from 'vitest';
import { api, NetworkWriteError, ApiError } from '../src/cloud/client';
import { queryClient, resetForIdentityChange } from '../src/cloud/queryClient';

// R7 F-12: how a write that got no HTTP answer is classified.
/** Resolves with the rejection value; a fulfilled promise is a test failure. */
const rejection = <T = NetworkWriteError>(p: Promise<unknown>): Promise<T> => p.then(() => { throw new Error('expected rejection'); }, (x: unknown) => x as T);
const setOnline = (v: boolean) => Object.defineProperty(globalThis, 'navigator', { value: { onLine: v }, configurable: true });
afterEach(() => { vi.unstubAllGlobals(); setOnline(true); });

describe('R7 F-12 写请求的网络结果分类', () => {
  it('离线：不调用 fetch，sent=false（服务器确定未变）', async () => {
    setOnline(false);
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const e = await rejection(api('assets', { method: 'POST', body: {} }));
    expect(e).toBeInstanceOf(NetworkWriteError);
    expect(e.sent).toBe(false);
    expect(fetch).not.toHaveBeenCalled();
  });
  it('超时 / 连接重置：sent=true（结果未知），文案不声称回滚', async () => {
    setOnline(true);
    for (const err of [new DOMException('timeout', 'TimeoutError'), new TypeError('Failed to fetch'), new DOMException('aborted', 'AbortError')]) {
      vi.stubGlobal('fetch', vi.fn().mockRejectedValue(err));
      const e = await rejection(api('assets/A1', { method: 'PATCH', body: {}, ifMatch: 1 }));
      expect(e).toBeInstanceOf(NetworkWriteError);
      expect(e.sent).toBe(true);
      expect(e.message).toContain('结果未知');
      expect(e.message).toContain('不会撤销');
    }
  });
  it('2xx 但响应体丢失：sent=true（服务器已写）', async () => {
    setOnline(true);
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, status: 200, json: () => Promise.reject(new TypeError('body stream')) }));
    const e = await rejection(api('assets/A1', { method: 'PATCH', body: {}, ifMatch: 1 }));
    expect(e).toBeInstanceOf(NetworkWriteError);
    expect(e.sent).toBe(true);
  });
  it('读请求的网络错误不改写为写入错误；HTTP 错误仍是 ApiError', async () => {
    setOnline(true);
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));
    expect(await rejection(api('assets'))).toBeInstanceOf(TypeError);
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 409, json: () => Promise.resolve({ error: 'version conflict' }) }));
    expect(await rejection(api('assets/A1', { method: 'PATCH', body: {}, ifMatch: 1 }))).toBeInstanceOf(ApiError);
  });
  it('mutation 默认 networkMode=always 且不重试；身份变化清空待执行 mutation', () => {
    expect(queryClient.getDefaultOptions().mutations).toMatchObject({ networkMode: 'always', retry: 0 });
    queryClient.getMutationCache().build(queryClient, { mutationFn: async () => 1 });
    expect(queryClient.getMutationCache().getAll().length).toBeGreaterThan(0);
    resetForIdentityChange();
    expect(queryClient.getMutationCache().getAll()).toHaveLength(0);
  });
});
