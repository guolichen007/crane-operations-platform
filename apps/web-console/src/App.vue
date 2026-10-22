<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue';
import CloudStatus from './components/CloudStatus.vue';
const cloudMode = import.meta.env.VITE_DATA_MODE === 'api';
import { useRouter, useRoute } from 'vue-router';
import {
  Layers3,
  Map,
  FolderKanban,
  Container,
  Activity,
  TriangleAlert,
  ChartNoAxesCombined,
  Wrench,
  Radio,
  Maximize,
  Play,
  UserRound,
  Command,
} from 'lucide-vue-next';
const router = useRouter(),
  route = useRoute(),
  clock = ref('');
let timer: ReturnType<typeof setInterval>;
const nav = [
  { path: 'overview', name: '态势沙盘', icon: Map },
  { path: 'projects', name: '项目中心', icon: FolderKanban },
  { path: 'devices', name: '设备中心', icon: Container },
  { path: 'monitor', name: '实时监控', icon: Activity },
  { path: 'alarms', name: '告警中心', icon: TriangleAlert },
  { path: 'analytics', name: '运行分析', icon: ChartNoAxesCombined },
  { path: 'maintenance', name: '运维计划', icon: Wrench },
  { path: 'remote', name: '远程运维', icon: Radio },
];
function tick() {
  clock.value = new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
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
async function tour() {
  await router.push('/overview');
  setTimeout(() => window.dispatchEvent(new Event('start-tour')), 100);
}
async function fullscreen() {
  if (document.fullscreenElement) await document.exitFullscreen();
  else await document.documentElement.requestFullscreen();
}
</script>
<template>
  <CloudStatus v-if="cloudMode" />
  <div class="app-shell">
    <aside class="rail">
      <RouterLink to="/overview" class="brand" aria-label="运营平台首页">
        <Layers3 :size="25" />
      </RouterLink>
      <nav>
        <RouterLink
          v-for="item in nav"
          :key="item.path"
          :to="'/' + item.path"
          :class="{ active: route.path === '/' + item.path }"
          :title="item.name"
        >
          <component :is="item.icon" :size="20" /><span>{{ item.name }}</span>
        </RouterLink>
      </nav>
      <div class="rail-bottom"><Command :size="18" /><span>V0.3.3</span></div>
    </aside>
    <div class="application">
      <header class="topbar">
        <div class="platform-title">
          全国行车设备运营平台 <small>SPATIAL OPERATIONS CONSOLE</small>
        </div>
        <div class="header-actions">
          <time>{{ clock }}</time
          ><span class="mode-tag"><i></i>演示模式</span
          ><button class="tour-trigger" @click="tour"><Play :size="13" />自动巡演</button
          ><button class="icon-button" title="全屏" aria-label="全屏" @click="fullscreen">
            <Maximize :size="17" /></button
          ><span class="user-avatar"><UserRound :size="17" /></span>
        </div>
      </header>
      <RouterView />
    </div>
  </div>
</template>
