import { defineStore } from 'pinia';
import { ref } from 'vue';
import type { User } from './model';
export const useApp = defineStore('app', () => {
  const user = ref<User | null>(null),
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
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 20000);
    try {
      const r = await fetch(url, {
        method,
        credentials: 'same-origin',
        signal: controller.signal,
        headers: {
          'Content-Type': 'application/json',
          ...(user.value ? { 'X-CSRF-Token': user.value.csrfToken } : {}),
        },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      const b = await r.json();
      if (!r.ok) throw new Error(b.message || '请求失败，请稍后重试');
      return b as T;
    } catch (e) {
      if (e instanceof Error && e.name === 'AbortError')
        throw new Error('请求超时，请保留输入并重试');
      throw e;
    } finally {
      clearTimeout(timer);
    }
  }
  async function loadUser() {
    try {
      user.value = await api<User>('/api/v1/me');
    } catch {
      user.value = null;
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
  return { user, message, error, api, toast, loadUser, confirmOpen, confirmDiscard, answer };
});
