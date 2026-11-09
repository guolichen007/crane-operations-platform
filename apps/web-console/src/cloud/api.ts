import type {
  Envelope,
  LiveSession,
  Shadow,
  InventoryProject,
  AssetDevice,
  OperationsSummary,
} from '../../../../contracts/typescript/types.gen';
import { csrfHeader, clearWebSession, productionAuth } from './session';

export type CloudAlarm = Extract<Envelope, { kind: 'ALARM' }>;
export const cloudMode = import.meta.env.VITE_DATA_MODE === 'api';

/** A failed API call; `status` is the HTTP status (0 = network / aborted before a response). */
export class ApiError extends Error {
  constructor(message: string, readonly status: number) {
    super(message);
  }
}

async function request<T>(path: string, method = 'GET', keepalive = false, signal?: AbortSignal): Promise<T> {
  // Unload cleanup is best effort; ordinary requests must not hang indefinitely.
  const timeout = keepalive ? undefined : AbortSignal.timeout(10000);
  const response = await fetch(`/api/v1/${path}`, {
    method,
    credentials: 'same-origin',
    keepalive,
    ...(method === 'GET' || method === 'HEAD' ? {} : { headers: await csrfHeader() }),
    ...(timeout ? { signal: signal ? AbortSignal.any([timeout, signal]) : timeout } : {}),
  });
  if (method === 'DELETE' && response.status === 404) return undefined as T;
  if (response.status === 401 && productionAuth) clearWebSession();
  if (!response.ok)
    throw new ApiError(
      response.status === 401 ? (productionAuth ? '请登录后访问云端' : '请先使用底部开发凭据连接云端')
        : response.status === 403 ? '权限不足' : `云端请求失败 (${response.status})`,
      response.status
    );
  return response.status === 204 ? (undefined as T) : response.json();
}
const segment = encodeURIComponent;
export const listDevices = () => request<Shadow[]>('devices');
export const listProjects = () => request<InventoryProject[]>('projects');
export const listAssets = () => request<AssetDevice[]>('assets/devices');
export const getDevice = (id: string) => request<Shadow>(`devices/${segment(id)}/shadow`);
export const listAlarms = () => request<CloudAlarm[]>('alarms');
export type AlarmWindow = OperationsSummary['freshness']['alarmWindow'];
/**
 * Authoritative operations summary (H2-1C, GET /api/v1/operations/summary). `region` null = the whole
 * authorized scope (country). The caller passes an AbortSignal so a superseded scope is cancelled.
 */
export const getOperationsSummary = (region: number | null, alarmWindow: AlarmWindow, signal?: AbortSignal) => {
  const q = new URLSearchParams();
  if (region !== null) q.set('region', String(region));
  q.set('alarmWindow', alarmWindow);
  return request<OperationsSummary>(`operations/summary?${q}`, 'GET', false, signal);
};
export const openLive = (id: string) =>
  request<LiveSession>(`devices/${segment(id)}/live-sessions`, 'POST');
export const renewLive = (id: string) =>
  request<LiveSession>(`live-sessions/${segment(id)}/renew`, 'POST');
export const closeLive = (id: string, keepalive = false) =>
  request<void>(`live-sessions/${segment(id)}`, 'DELETE', keepalive);
export const streamUrl = (id: string) =>
  `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/api/v1/devices/${segment(id)}/stream`;
