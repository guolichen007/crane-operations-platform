import { ApiError, request } from './api-client';
import { defineStore } from 'pinia';
import { ref } from 'vue';
import type { User } from './model';
import { clearStaffDraft } from './staff-role-draft';

// Cross-tab session sync: a logout in one tab signs every tab of this browser out.
const channelName = 'baobei.session';
type SessionMessage = { type: 'signed-out' } | { type: 'signed-in'; subject: string };

export const useApp = defineStore('app', () => {
  const user = ref<User | null>(null),
    sessionChecked = ref(false),
    sessionIssue = ref<'expired' | 'network' | 'unavailable' | 'signed-out' | 'switched' | null>(null),
    message = ref(''),
    error = ref(''),
    confirmOpen = ref(false);
  let resolveConfirm: ((v: boolean) => void) | undefined;
  let toastTimer: ReturnType<typeof setTimeout>;
  const channel = typeof BroadcastChannel === 'function' ? new BroadcastChannel(channelName) : null;
  function broadcast(m: SessionMessage) {
    try { channel?.postMessage(m); } catch { /* best effort */ }
  }
  channel?.addEventListener('message', (event: MessageEvent<SessionMessage>) => {
    const m = event.data;
    if (m?.type === 'signed-out' && user.value) {
      user.value = null;
      sessionIssue.value = 'signed-out';
      clearStaffDraft();
    } else if (m?.type === 'signed-in' && user.value && user.value.subject !== m.subject) {
      // Another tab signed in as a different person; this tab's identity is stale.
      user.value = null;
      sessionIssue.value = 'switched';
    }
  });
  function toast(s: string) {
    message.value = s;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => (message.value = ''), 4500);
  }
  async function api<T>(url: string, method = 'GET', body?: unknown, idempotencyKey = ''): Promise<T> {
    const current = user.value;
    try {
      return await request<T>(url, method, body, current?.csrfToken || '', 20000, idempotencyKey);
    } catch (e) {
      if (current && user.value === current && e instanceof ApiError && e.status === 401 && url.startsWith('/api/')) {
        user.value = null;
        sessionIssue.value = 'expired';
      }
      throw e;
    }
  }
  async function loadUser() {
    try {
      user.value = await request<User>('/api/v1/me');
      sessionIssue.value = null;
      broadcast({ type: 'signed-in', subject: user.value.subject });
    } catch (e) {
      user.value = null;
      if (e instanceof ApiError && e.status === 401) {
        sessionIssue.value = e.code === 'SESSION_EXPIRED' ? 'expired' : null;
      } else {
        sessionIssue.value = e instanceof ApiError && e.status === 0 ? 'network' : 'unavailable';
      }
    } finally {
      sessionChecked.value = true;
    }
  }
  // scope 'local' ends the Baobei session only. 'sso' also returns Keycloak's
  // end-session URL, which signs the enterprise account out of every application.
  async function logout(scope: 'local' | 'sso' = 'local'): Promise<string | undefined> {
    const csrf = user.value?.csrfToken || '';
    const result = await request<{ ok: boolean; endSessionUrl?: string }>(
      '/auth/logout' + (scope === 'sso' ? '?scope=sso' : ''), 'POST', {}, csrf);
    clearStaffDraft();
    user.value = null;
    sessionIssue.value = 'signed-out';
    broadcast({ type: 'signed-out' });
    return result.endSessionUrl;
  }
  function confirmDiscard() {
    confirmOpen.value = true;
    return new Promise<boolean>((r) => (resolveConfirm = r));
  }
  function answer(v: boolean) {
    confirmOpen.value = false;
    resolveConfirm?.(v);
    resolveConfirm = undefined;
  }
  return { user, sessionChecked, sessionIssue, message, error, api, toast, loadUser, logout, confirmOpen, confirmDiscard, answer };
});
