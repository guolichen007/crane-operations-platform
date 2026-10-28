<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import { queryWebSession, logoutWebSession, webSession } from '../cloud/session';
const error = ref(''), loading = ref(true);
function expired() { error.value = ''; }
async function refresh() {
  loading.value = true;
  try { await queryWebSession(); error.value = ''; }
  catch (failure) { error.value = (failure as Error).message; }
  finally { loading.value = false; }
}
async function logout() {
  try {
    const redirect = await logoutWebSession();
    if (redirect) location.assign(redirect);
  } catch (failure) { error.value = (failure as Error).message; }
}
onMounted(() => { window.addEventListener('cloud-session-expired', expired); void refresh(); });
onUnmounted(() => window.removeEventListener('cloud-session-expired', expired));
</script>
<template>
  <section class="cloud-status production-session" aria-label="用户会话" data-testid="production-session">
    <span v-if="loading">正在查询登录状态</span>
    <template v-else-if="webSession">
      <span data-testid="session-user">{{ webSession.displayName || webSession.userId }}</span>
      <span v-if="!webSession.projects.length">暂无项目授权，请联系管理员</span>
      <span v-else data-testid="session-projects">授权项目：{{ webSession.projects.map(project => project.projectName).join('、') }}</span>
      <button type="button" @click="logout">退出</button>
    </template>
    <a v-else href="/auth/login">登录</a>
    <span v-if="error" role="alert">{{ error }}</span>
  </section>
</template>
<style scoped>
.production-session { position: fixed; bottom: 12px; left: 80px; right: 12px; z-index: 100; display: flex; align-items: center; gap: 18px; padding: 10px 16px; border: 1px solid #39606b; background: #122b35f5; color: #b9d4dd; font-size: 12px; }
.production-session a { color: #72dcdd; }
[data-testid='session-projects'] { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
