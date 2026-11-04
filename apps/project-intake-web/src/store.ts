import { ApiError, request } from './api-client';
import { defineStore } from 'pinia';
import { ref } from 'vue';
import type { User } from './model';
export const useApp = defineStore('app', () => {
  const user = ref<User | null>(null),
    sessionChecked = ref(false),
    sessionIssue = ref<'expired' | 'network' | 'unavailable' | null>(null),
    message = ref(''),
    error = ref(''),
    confirmOpen = ref(false);
  let resolveConfirm: ((v: boolean) => void) | undefined;
  let toastTimer: ReturnType<typeof setTimeout>;
  function toast(s: string) {
    message.value = s;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => (message.value = ''), 4500);
  }
  async function api<T>(url: string, method = 'GET', body?: unknown): Promise<T> {
    const current = user.value;
    try {
      return await request<T>(url, method, body, current?.csrfToken || '');
    } catch (e) {
      if (current && user.value === current && e instanceof ApiError && e.status === 401 &&
          (url.startsWith('/api/') || url === '/auth/logout')) {
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
  function confirmDiscard() {
    confirmOpen.value = true;
    return new Promise<boolean>((r) => (resolveConfirm = r));
  }
  function answer(v: boolean) {
    confirmOpen.value = false;
    resolveConfirm?.(v);
    resolveConfirm = undefined;
  }
  return { user, sessionChecked, sessionIssue, message, error, api, toast, loadUser, confirmOpen, confirmDiscard, answer };
});
