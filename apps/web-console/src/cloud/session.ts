import { shallowRef } from 'vue';
import type { WebSession } from '../../../../contracts/typescript/types.gen';

export const productionAuth = import.meta.env.VITE_AUTH_MODE === 'oidc';
export const webSession = shallowRef<WebSession | null>(null);
let pending: Promise<WebSession | null> | undefined;
let generation = 0;

export function clearWebSession() {
  generation++;
  webSession.value = null;
  pending = undefined;
  window.dispatchEvent(new Event('cloud-session-expired'));
}

// Only the CSRF token is JS-readable; the opaque authentication Cookie is HttpOnly.
// No OAuth token, authentication Cookie or client secret is put in Web Storage.
export function queryWebSession(): Promise<WebSession | null> {
  if (pending) return pending;
  const currentGeneration = generation;
  return (pending = (async () => {
    try {
      const response = await fetch('/auth/session', {
        credentials: 'same-origin', cache: 'no-store', signal: AbortSignal.timeout(10000),
      });
      if (currentGeneration !== generation) return null;
      if (response.status === 401) {
        clearWebSession();
        return null;
      }
      if (!response.ok) throw Error('登录会话暂不可用');
      const value = await response.json() as WebSession;
      if (currentGeneration !== generation) return null;
      webSession.value = value;
      return webSession.value;
    } finally {
      if (currentGeneration === generation) pending = undefined;
    }
  })());
}

export async function csrfHeader(): Promise<Record<string, string>> {
  if (!productionAuth) return {};
  const current = webSession.value ?? await queryWebSession();
  if (!current) throw Error('请登录后访问云端');
  return { 'X-CSRF-Token': current.csrfToken };
}

export function canWatchProject(projectId: string) {
  if (!productionAuth) return true;
  const role = webSession.value?.projects.find(project => project.projectId === projectId)?.role;
  return role === 'operator' || role === 'maintainer' || role === 'platform_admin';
}

export async function logoutWebSession() {
  const headers = await csrfHeader();
  const response = await fetch('/auth/logout', {
    method: 'POST', credentials: 'same-origin', headers, signal: AbortSignal.timeout(10000),
  });
  if (!response.ok && response.status !== 401) throw Error(response.status === 403 ? '权限不足，退出请求被拒绝' : '退出暂不可用');
  clearWebSession();
  return response.headers.get('Location');
}
