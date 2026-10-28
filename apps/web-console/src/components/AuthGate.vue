<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from 'vue';
import { productionAuth, queryWebSession, webSession, clearWebSession } from '../cloud/session';
import { authState } from '../cloud/auth-state';
import BrandLoginCover from './BrandLoginCover.vue';
const loading = ref(productionAuth), error = ref('');
const state = computed(() => authState(loading.value, webSession.value, error.value));
let alive = true, timer: ReturnType<typeof setInterval>;
async function refresh() {
  loading.value = true; error.value = '';
  try { await queryWebSession(); }
  catch { if (alive) error.value = '登录服务暂不可用，请稍后重试。'; }
  finally { if (alive) loading.value = false; }
}
function expired() { loading.value = false; error.value = ''; }
function checkExpiry() {
  const session = webSession.value;
  if (session && Math.min(Date.parse(session.expiresAt), Date.parse(session.idleExpiresAt)) <= Date.now()) clearWebSession();
}
onMounted(() => {
  if (!productionAuth) return;
  window.addEventListener('cloud-session-expired', expired);
  void refresh();
  timer = setInterval(checkExpiry, 1000);
});
onUnmounted(() => { alive = false; clearInterval(timer); window.removeEventListener('cloud-session-expired', expired); });
</script>
<template>
  <slot v-if="!productionAuth || state === 'AUTHENTICATED'" />
  <BrandLoginCover v-else :state="state" :error="error" @retry="refresh" />
</template>
