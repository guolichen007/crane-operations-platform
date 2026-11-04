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
  if (STATE_CHANGING.has(method)) {
    Object.assign(headers, await csrfHeader());
    // Trace id: a short caller-supplied id used only for audit correlation. The
    // server validates it and falls back to its own id when missing/invalid.
    headers['X-Request-Id'] = requestId();
  }

  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(options.query ?? {}))
    if (value !== undefined && value !== '') params.set(key, String(value));
  const qs = params.size ? `?${params.toString()}` : '';

  const response = await fetch(`/api/v1/${path}${qs}`, {
    method,
    credentials: 'same-origin',
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
    signal: AbortSignal.timeout(20_000),
  });
  if (response.status === 401 && productionAuth) clearWebSession();
  if (!response.ok) {
    const body = (await response.json().catch(() => ({}))) as ApiErrorBody;
    const code = body.code ?? STATUS_CODE[response.status] ?? 'UNKNOWN_ERROR';
    const message = body.error ?? body.message ?? `请求失败 (${response.status})`;
    throw new ApiError(response.status, code, message);
  }
  return response.status === 204 ? (undefined as T) : ((await response.json()) as T);
}

export function pageQuery(limit: number, offset: number, sort?: string, q?: string) {
  return { limit, offset, sort, q };
}
