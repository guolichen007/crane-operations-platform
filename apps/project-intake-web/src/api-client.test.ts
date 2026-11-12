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
  it('创建请求必须显式携带意图键且原样发送', async () => {
    const seen: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (_u: string, init: RequestInit) => {
      seen.push(String((init.headers as Record<string, string>)['Idempotency-Key']));
      return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } });
    }));
    await expect(request('/api/v1/projects', 'POST', {})).rejects.toThrow('缺少创建意图标识');
    const intent = 'intent-' + 'x'.repeat(16); // synthetic creation intent, not a credential
    await request('/api/v1/projects', 'POST', {}, '', 1000, intent);
    await request('/api/v1/projects', 'POST', {}, '', 1000, intent);
    expect(seen).toEqual([intent, intent]);
  });
});

describe('结构化字段错误（G5-D1）', () => {
  it('details.field 传给调用方，旧响应无 field 时保持兼容', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ code: 'REASON_REQUIRED', message: '请填写废弃原因', details: { field: 'reason' } }), { status: 400, headers: { 'Content-Type': 'application/json' } })));
    await expect(request('/api/x', 'POST', {})).rejects.toMatchObject({ status: 400, code: 'REASON_REQUIRED', field: 'reason', message: '请填写废弃原因' });
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ code: 'INVALID_INPUT', message: '请求字段不正确', details: {} }), { status: 400, headers: { 'Content-Type': 'application/json' } })));
    await expect(request('/api/x', 'POST', {})).rejects.toMatchObject({ code: 'INVALID_INPUT', field: '' });
  });
});
