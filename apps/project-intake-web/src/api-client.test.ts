import { describe, it, expect, vi, afterEach } from 'vitest';
import { request } from './api-client';
afterEach(() => vi.unstubAllGlobals());
describe('生产 HTTP 失败模型', () => {
  for (const [status, body, type, message] of [
    [502, '<html>bad gateway</html>', 'text/html', '服务暂时不可用'],
    [504, '', 'text/plain', '服务响应超时'],
    [500, 'proxy failed', 'text/plain', '服务暂时不可用'],
    [200, '', 'text/plain', '服务返回格式异常'],
  ] as const) {
    it(String(status) + type, async () => {
      vi.stubGlobal(
        'fetch',
        vi.fn(async () => new Response(body, { status, headers: { 'Content-Type': type } }))
      );
      await expect(request('/api/test')).rejects.toThrow(message);
    });
  }
  it('网络失败', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('Failed to fetch');
      })
    );
    await expect(request('/api/test')).rejects.toThrow('网络连接失败');
  });
  it('超时', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        (_url, init) =>
          new Promise((_resolve, reject) =>
            init.signal.addEventListener('abort', () =>
              reject(new DOMException('aborted', 'AbortError'))
            )
          )
      )
    );
    await expect(request('/api/test', 'POST', {}, '', 5)).rejects.toThrow('请求超时');
  });
  it('JSON业务错误保留中文', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(JSON.stringify({ message: '版本冲突' }), {
            status: 409,
            headers: { 'Content-Type': 'application/json' },
          })
      )
    );
    await expect(request('/api/test')).rejects.toThrow('版本冲突');
  });
});
