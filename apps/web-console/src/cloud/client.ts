import { csrfHeader, clearWebSession, productionAuth } from './session';

export type ApiErrorBody = { error?: string; code?: string; message?: string };

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

/**
 * R7 F-12: a write that did not get an HTTP answer. `sent=false` is proven (the browser was offline,
 * so the request was never handed to the network): nothing happened on the server and the draft can
 * be resubmitted deliberately. `sent=true` means the request may have reached the server (connection
 * reset, timeout): the outcome is UNKNOWN — the caller must re-read the record (version / idempotent
 * replay) instead of assuming failure. Cancelling a request never rolls back a server transaction.
 */
export class NetworkWriteError extends Error {
  constructor(public sent: boolean) {
    super(
      sent
        ? '网络中断或请求超时，结果未知：请求可能已被服务器处理。请刷新核对后再决定是否重新提交（取消请求不会撤销服务器上已完成的操作）。'
        : '当前处于离线状态，请求未发送，服务器未做任何更改。输入已保留，恢复网络后请手动重新提交。',
    );
    this.name = 'NetworkWriteError';
  }
}

export type PageMeta = { limit: number; offset: number; total: number };
export type Paged<T> = { items: T[]; page: PageMeta };

export type RequestOptions = {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  body?: unknown;
  /** Optimistic-concurrency expected version (If-Match header). */
  ifMatch?: number;
  idempotencyKey?: string;
  query?: Record<string, string | number | boolean | undefined>;
};

const STATUS_CODE: Record<number, string> = {
  400: 'VALIDATION_ERROR',
  403: 'NO_PERMISSION',
  404: 'NOT_FOUND',
  428: 'PRECONDITION_REQUIRED',
  409: 'VERSION_CONFLICT',
  503: 'DATABASE_UNAVAILABLE',
};

const STATE_CHANGING = new Set(['POST', 'PUT', 'PATCH', 'DELETE']);

// A fresh, short trace id per write request. It is a tracking input only; the
// server validates and falls back to its own id when this one is invalid.
function requestId(): string {
  return crypto.randomUUID().replace(/-/g, '').slice(0, 20);
}

/**
 * Unified workspace API client: JSON + CSRF + 401/403 handling + If-Match +
 * Idempotency-Key + stable API error codes + pagination query serialization.
 * Pages must not scatter raw fetch calls.
 */
export async function api<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const method = options.method ?? 'GET';
  const headers: Record<string, string> = {};
  if (options.body !== undefined) headers['Content-Type'] = 'application/json';
  if (options.ifMatch !== undefined) headers['If-Match'] = `"${options.ifMatch}"`;
  if (options.idempotencyKey) headers['Idempotency-Key'] = options.idempotencyKey;
  const writing = STATE_CHANGING.has(method);
  // Offline: refuse before anything is sent. Never queue a write for "later".
  if (writing && typeof navigator !== 'undefined' && navigator.onLine === false) throw new NetworkWriteError(false);
  if (writing) {
    Object.assign(headers, await csrfHeader());
    // Trace id: a short caller-supplied id used only for audit correlation. The
    // server validates it and falls back to its own id when missing/invalid.
    headers['X-Request-Id'] = requestId();
  }

  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(options.query ?? {}))
    if (value !== undefined && value !== '') params.set(key, String(value));
  const qs = params.size ? `?${params.toString()}` : '';

  let response: Response;
  try {
    response = await fetch(`/api/v1/${path}${qs}`, {
      method,
      credentials: 'same-origin',
      headers,
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      signal: AbortSignal.timeout(20_000),
    });
  } catch (e) {
    if (!writing) throw e;
    // The browser was online when the write was handed to fetch, so it may have been delivered
    // (connection reset, timeout): the outcome is unknown, never "failed".
    throw new NetworkWriteError(true);
  }
  if (response.status === 401 && productionAuth) clearWebSession();
  if (!response.ok) {
    const body = (await response.json().catch(() => ({}))) as ApiErrorBody;
    const code = body.code ?? STATUS_CODE[response.status] ?? 'UNKNOWN_ERROR';
    const message = body.error ?? body.message ?? `请求失败 (${response.status})`;
    throw new ApiError(response.status, code, message);
  }
  if (response.status === 204) return undefined as T;
  try {
    return (await response.json()) as T;
  } catch (e) {
    // The server answered 2xx (the write happened) but the body was lost mid-stream.
    if (writing) throw new NetworkWriteError(true);
    throw e;
  }
}

export function pageQuery(limit: number, offset: number, sort?: string, q?: string) {
  return { limit, offset, sort, q };
}
