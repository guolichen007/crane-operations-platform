export async function request<T>(
  url: string,
  method = 'GET',
  body?: unknown,
  csrf = '',
  timeoutMs = 20000
): Promise<T> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const r = await fetch(url, {
      method,
      credentials: 'same-origin',
      signal: controller.signal,
      headers: { 'Content-Type': 'application/json', ...(csrf ? { 'X-CSRF-Token': csrf } : {}) },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    const text = await r.text();
    let value: unknown;
    if (r.headers.get('content-type')?.includes('application/json') && text) {
      try {
        value = JSON.parse(text);
      } catch {
        value = undefined;
      }
    }
    if (!r.ok) {
      let message =
        r.status === 504
          ? '服务响应超时，请稍后重试'
          : r.status >= 500
            ? '服务暂时不可用，请稍后重试'
            : r.status === 401
              ? '登录已失效，请重新登录'
              : '请求未完成，请刷新后重试';
      if (
        r.status !== 502 &&
        r.status !== 504 &&
        value &&
        typeof value === 'object' &&
        'message' in value &&
        typeof value.message === 'string'
      )
        message = value.message;
      throw new Error(message);
    }
    if (r.status === 204) return undefined as T;
    if (value === undefined) throw new Error('服务返回格式异常，请稍后重试');
    return value as T;
  } catch (e) {
    if (e instanceof Error && e.name === 'AbortError')
      throw new Error('请求超时，请保留输入并重试');
    if (e instanceof TypeError) throw new Error('网络连接失败，请检查网络后重试');
    throw e;
  } finally {
    clearTimeout(timer);
  }
}
