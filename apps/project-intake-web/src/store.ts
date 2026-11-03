import { request } from './api-client';
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
    return request<T>(url, method, body, user.value?.csrfToken || '');
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
