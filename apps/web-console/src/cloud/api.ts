import type {
  Envelope,
  LiveSession,
  Shadow,
  InventoryProject,
  AssetDevice,
} from '../../../../contracts/typescript/types.gen';
import { csrfHeader, clearWebSession, productionAuth } from './session';

export type CloudAlarm = Extract<Envelope, { kind: 'ALARM' }>;
export const cloudMode = import.meta.env.VITE_DATA_MODE === 'api';

async function request<T>(path: string, method = 'GET', keepalive = false): Promise<T> {
  const response = await fetch(`/api/v1/${path}`, {
    method,
    credentials: 'same-origin',
    keepalive,
    ...(method === 'GET' || method === 'HEAD' ? {} : { headers: await csrfHeader() }),
    // Unload cleanup is best effort; ordinary requests must not hang indefinitely.
    ...(keepalive ? {} : { signal: AbortSignal.timeout(10000) }),
  });
  if (method === 'DELETE' && response.status === 404) return undefined as T;
  if (response.status === 401 && productionAuth) clearWebSession();
  if (!response.ok)
    throw Error(
      response.status === 401 ? (productionAuth ? '请登录后访问云端' : '请先使用底部开发凭据连接云端')
        : response.status === 403 ? '权限不足' : `云端请求失败 (${response.status})`
    );
  return response.status === 204 ? (undefined as T) : response.json();
}
const segment = encodeURIComponent;
export const listDevices = () => request<Shadow[]>('devices');
export const listProjects = () => request<InventoryProject[]>('projects');
export const listAssets = () => request<AssetDevice[]>('assets/devices');
export const getDevice = (id: string) => request<Shadow>(`devices/${segment(id)}/shadow`);
export const listAlarms = () => request<CloudAlarm[]>('alarms');
export const openLive = (id: string) =>
  request<LiveSession>(`devices/${segment(id)}/live-sessions`, 'POST');
export const renewLive = (id: string) =>
  request<LiveSession>(`live-sessions/${segment(id)}/renew`, 'POST');
export const closeLive = (id: string, keepalive = false) =>
  request<void>(`live-sessions/${segment(id)}`, 'DELETE', keepalive);
export const streamUrl = (id: string) =>
  `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/api/v1/devices/${segment(id)}/stream`;
