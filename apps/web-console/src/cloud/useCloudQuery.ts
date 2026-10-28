import { onMounted, onUnmounted, ref, shallowRef } from 'vue';

export function useCloudQuery<T>(load: () => Promise<T>) {
  const data = shallowRef<T>(),
    error = ref(''),
    loading = ref(true);
  let disposed = false,
    version = 0,
    timer: ReturnType<typeof setInterval>;
  async function refresh() {
    const request = ++version;
    try {
      const result = await load();
      if (disposed || request !== version) return;
      data.value = result;
      error.value = '';
    } catch (e) {
      if (!disposed && request === version) error.value = (e as Error).message;
    } finally {
      if (!disposed && request === version) loading.value = false;
    }
  }
  function invalidate() {
    ++version;
    data.value = undefined;
    error.value = '请登录后访问云端';
    loading.value = false;
  }
  onMounted(() => {
    void refresh();
    timer = setInterval(() => void refresh(), 15000);
    window.addEventListener('cloud-authenticated', refresh);
    window.addEventListener('cloud-session-expired', invalidate);
  });
  onUnmounted(() => {
    disposed = true;
    version++;
    clearInterval(timer);
    window.removeEventListener('cloud-authenticated', refresh);
    window.removeEventListener('cloud-session-expired', invalidate);
  });
  return { data, error, loading, refresh };
}
