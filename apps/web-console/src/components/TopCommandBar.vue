<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { ChevronDown, LogOut, Maximize, ShieldCheck, UserRound, Play } from 'lucide-vue-next';
import BrandMark from './BrandMark.vue';
import { webSession, productionAuth, logoutWebSession } from '../cloud/session';
import { isViewerOnly } from '../cloud/auth-state';
import { scopeMode } from '../cloud/project-scope';
const route = useRoute(),
  router = useRouter(),
  clock = ref(''),
  error = ref('');
const viewer = computed(() => isViewerOnly(webSession.value));
const scope = computed(() => (typeof route.query.project === 'string' ? route.query.project : ''));
// Only pages that really use ?project= offer the project selector; on the others
// the data is always "all authorized projects" and the bar says so. On the
// overview the selector focuses the map (it does not hide other projects), so it
// is labelled 聚焦项目 rather than 项目范围.
const mode = computed(() => scopeMode(route.path));
const scopeApplies = computed(() => mode.value !== 'none');
const scopeLabel = computed(() => (mode.value === 'focus' ? '地图聚焦项目' : '当前项目范围'));
const allLabel = computed(() => (mode.value === 'focus' ? '不聚焦（全部已授权项目）' : '全部已授权项目'));
let timer: ReturnType<typeof setInterval>;
function tick() {
  clock.value = new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(new Date());
}
onMounted(() => {
  tick();
  timer = setInterval(tick, 1000);
});
onUnmounted(() => clearInterval(timer));
async function logout() {
  try {
    const redirect = await logoutWebSession();
    if (redirect) location.assign(redirect);
  } catch (e) {
    error.value = (e as Error).message;
  }
}
async function fullscreen() {
  if (document.fullscreenElement) await document.exitFullscreen();
  else await document.documentElement.requestFullscreen();
}
function changeScope(event: Event) {
  const project = (event.target as HTMLSelectElement).value;
  // Preserve the current business page: only the ?project= scope changes, so
  // switching projects never forces a jump back to /projects.
  const query = { ...route.query };
  if (project) query.project = project;
  else delete query.project;
  void router.push({ path: route.path, query });
}
async function tour() {
  await router.push('/overview');
  window.dispatchEvent(new Event('start-tour'));
}
</script>
<template>
  <header class="command-bar" data-testid="production-session">
    <RouterLink to="/overview" class="command-brand"
      ><BrandMark />
      <div>
        <b>友道智造 <span>/</span> 全国行车设备运营平台</b><small>CRANE OPERATIONS CENTER</small>
      </div></RouterLink
    >
    <div class="command-scope">
      <span class="scope-divider"></span
      ><template v-if="scopeApplies"
        ><select :value="scope" :aria-label="scopeLabel" :title="scopeLabel" :data-scope-mode="mode" @change="changeScope">
          <option value="">{{ allLabel }}</option>
          <option v-for="p in webSession?.projects" :key="p.projectId" :value="p.projectId">
            {{ p.projectName }}
          </option></select
        ><ChevronDown :size="12"
      /></template
      ><span v-else class="scope-fixed" data-testid="scope-fixed" title="此页面不按项目筛选，始终显示全部已授权项目"
        >全部已授权项目</span
      >
    </div>
    <div class="command-actions">
      <button
        v-if="!productionAuth && route.path === '/overview'"
        class="tour-trigger"
        @click="tour"
      >
        <Play :size="12" />自动巡演
      </button>
      <span class="connection-indicator"
        ><i></i>{{ productionAuth ? '会话已验证' : 'DEMO / 演示环境' }}</span
      ><time>{{ clock }}</time
      ><button class="icon-button" aria-label="全屏" @click="fullscreen">
        <Maximize :size="16" /></button
      ><span v-if="viewer" class="viewer-badge" data-testid="viewer-only"
        ><ShieldCheck :size="13" />只读观察</span
      ><span class="command-avatar"><UserRound :size="17" /></span>
      <div class="command-user">
        <b data-testid="session-user">{{
          webSession?.displayName || webSession?.userId || '演示访客'
        }}</b
        ><small>{{
          viewer ? '项目观察员' : productionAuth ? '按项目授权' : 'DEMO WORKSPACE'
        }}</small>
      </div>
      <button v-if="productionAuth" class="logout-button" aria-label="退出" @click="logout">
        <LogOut :size="15" />
      </button>
    </div>
    <span v-if="error" class="command-error" role="alert">{{ error }}</span>
    <span class="sr-only" data-testid="session-projects">{{
      webSession?.projects.map((p) => p.projectName).join('、')
    }}</span>
  </header>
</template>
