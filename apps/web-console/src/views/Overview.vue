<script setup lang="ts">
// Operations Center homepage (H1). Layout shell only: business state lives in the
// operations store, URL sync in useHomeUrlSync, numbers in scene/home-metrics,
// rendering in SpatialStage3D (unchanged MapSceneManager, dynamic import) or
// SpatialStage2D (SVG). See docs/04-前端与可视化/Homepage-H1-0-高保真布局设计.md.
import { computed, defineAsyncComponent, onMounted, onUnmounted, ref, watch } from 'vue';
import './overview/home.css';
import { storeToRefs } from 'pinia';
import { RefreshCw, X } from 'lucide-vue-next';
import { useRoute, useRouter } from 'vue-router';
import { useOperations } from '../stores/operations';
import { sceneProvider } from '../scene/provider';
import { homeMetrics, homeKpis, hasLocation, homeDataState, hasNumbers, scopeLabel } from '../scene/home-metrics';
import { formatCloudTime } from '../cloud/format';
import { cloudMode } from '../cloud/api';
import CloudRuntime from '../components/CloudRuntime.vue';
import KpiStrip from './overview/KpiStrip.vue';
import AttentionPanel from './overview/AttentionPanel.vue';
import InspectorPanel from './overview/InspectorPanel.vue';
import EventStrip from './overview/EventStrip.vue';
import RegionNavPanel from './overview/RegionNavPanel.vue';
import SpatialStage2D from './overview/SpatialStage2D.vue';
import { useHomeUrlSync } from './overview/useHomeUrlSync';
const SpatialStage3D = defineAsyncComponent(() => import('./overview/SpatialStage3D.vue'));

const store = useOperations();
const route = useRoute();
const router = useRouter();
const { source, status, view, webglUnavailable, notice, regionStack, sceneState, cameraPreset, tour, selected, selectedProject, dataState } =
  storeToRefs(store);
const demo = sceneProvider.mode === 'demo';
useHomeUrlSync();

// R1-1: one data state for every homepage number. Numbers exist only after a
// successful fetch; a failed first fetch is "unavailable", not 0.
watch(
  () =>
    homeDataState({
      mode: sceneProvider.mode,
      loading: sceneProvider.loading.value,
      error: sceneProvider.error.value,
      fetchedAt: sceneProvider.fetchedAt.value,
    }),
  (s) => (dataState.value = s),
  { immediate: true }
);
// R1-2: KPI / Attention base set = authorized ∩ source ∩ displayed region (scope
// contract). The status filter and the selection never change these numbers.
const metrics = computed(() =>
  hasNumbers(dataState.value) ? homeMetrics(store.scopedProjects, store.scopedDevices) : null
);
const plannedDevices = computed(() =>
  store.scopedProjects.filter((p) => p.dataSource === 'real').reduce((s, p) => s + p.quantity, 0)
);
const kpis = computed(() =>
  homeKpis(demo ? 'demo' : 'api', metrics.value, sceneProvider.alarmRecordCount.value, plannedDevices.value)
);
const scopeText = computed(() => scopeLabel(demo ? 'demo' : 'api', store.scope, store.scopeName));
// R1-3: fetchedAt is the client's fetch time, not a device observation time.
const fetchedText = computed(() => formatCloudTime(sceneProvider.fetchedAt.value));
const asOf = computed(() => {
  if (demo) return '本地演示数据 · 非遥测';
  switch (dataState.value) {
    case 'loading':
      return '正在拉取 Inventory API…';
    case 'unavailable':
      return 'Inventory API 拉取失败 · 暂无可用数据';
    case 'refreshing':
      return `最近拉取 ${fetchedText.value} · 正在更新…`;
    case 'stale':
      return `最近成功拉取 ${fetchedText.value} · 更新失败，数据已过期`;
    default:
      return `最近拉取 ${fetchedText.value} · 来源 Inventory API`;
  }
});
const providerError = computed(() => sceneProvider.error.value);
const noProjects = computed(() => dataState.value === 'ready' && sceneProvider.projects.value.length === 0);
function reload() {
  sceneProvider.refresh().catch(() => undefined);
}
/** KPI click: a status filter, or — for the Inventory card — the Inventory device workflow in the same scope. */
function onKpi(filter: string, id: string) {
  if (!demo && id === 'devices') {
    const region = store.regionPath[store.regionPath.length - 1];
    void router.push({ path: '/devices', query: region ? { region: String(region) } : {} });
    return;
  }
  applyFilter(filter);
}
function applyFilter(filter: string) {
  status.value = filter as typeof status.value;
}
function setView(next: '2d' | '3d') {
  if (next === '3d' && webglUnavailable.value) return;
  view.value = next;
}
watch(webglUnavailable, (v) => {
  if (v) view.value = '2d';
});

// Breakpoint: below 1600 the Inspector is a drawer shown only with a selection and
// Attention is a narrow strip, so common laptop widths (1280–1536) keep the map as
// the largest element (H1-0 design §3).
const narrow = ref(false);
const mq = matchMedia('(max-width: 1599px)');
const onMq = () => (narrow.value = mq.matches);
onMounted(() => {
  // The page owns the data load (not the 3D stage): the 2D view and the WebGL
  // fallback must get numbers too (found in R1-1 — before, ?view=2d stayed LOADING).
  void sceneProvider.ensureLoaded().catch(() => undefined);
  onMq();
  mq.addEventListener('change', onMq);
  if (import.meta.env.DEV) (window as unknown as { __routeQuery?: () => unknown }).__routeQuery = () => route.query;
});
onUnmounted(() => mq.removeEventListener('change', onMq));
// The overlay drawer covers the stage's right edge at narrow widths; the 3D camera
// keeps the focused target clear of it (MapSceneManager.setInspectorWidth).
const drawerOpen = computed(() => narrow.value && (!!selected.value || !!selectedProject.value));
watch(drawerOpen, (open) => (store.inspectorInset = open ? 320 : 0), { immediate: true });
</script>
<template>
  <main
    class="overview home-oc"
    :data-scene-level="regionStack[regionStack.length - 1]?.level"
    :data-adcode="regionStack[regionStack.length - 1]?.adcode"
    :data-scene-state="sceneState"
    :data-camera-preset="cameraPreset"
    :data-source-filter="source"
    :data-status-filter="status"
    :data-view="view"
    :data-tour="tour"
  >
    <header class="overview-intro home-head">
      <h1>全国设备运营态势<span>空间资产 · 协同运营</span></h1>
      <span
        class="home-asof"
        :class="'state-' + dataState"
        data-testid="home-asof"
        :data-state="dataState"
        title="客户端最近一次成功拉取 Inventory API 的时间，不是设备遥测的观测时间"
      >{{ asOf }}</span>
      <button
        v-if="!demo"
        type="button"
        class="home-refresh"
        data-testid="home-refresh"
        :disabled="dataState === 'loading' || dataState === 'refreshing'"
        aria-label="重新拉取首页数据"
        title="重新拉取首页数据"
        @click="reload"
      ><RefreshCw :size="13" /></button>
      <span class="home-scope" data-testid="kpi-scope" title="KPI 与「需要关注」的统计范围；状态过滤只影响地图与列表展示，不改变指标">
        统计范围 <b>{{ scopeText }}</b><i v-if="dataState === 'stale'" class="home-stale-tag" data-testid="kpi-stale">已过期</i>
      </span>
      <div class="home-view-switch" role="group" aria-label="空间视图">
        <button type="button" :class="{ selected: view === '2d' }" :aria-pressed="view === '2d'" @click="setView('2d')">轻量视图</button>
        <button
          type="button"
          :class="{ selected: view === '3d' }"
          :aria-pressed="view === '3d'"
          :disabled="!!webglUnavailable"
          :title="webglUnavailable || '三维数字沙盘'"
          @click="setView('3d')"
        >三维沙盘</button>
      </div>
    </header>
    <p v-if="notice" class="home-notice" role="status" data-testid="home-notice">
      {{ notice }}<button type="button" aria-label="关闭提示" @click="notice = ''"><X :size="12" /></button>
    </p>
    <p v-if="webglUnavailable" class="home-notice warn scene-error" role="alert" data-testid="webgl-fallback">
      三维沙盘不可用（{{ webglUnavailable }}），已切换到轻量视图。
    </p>
    <p v-if="providerError && dataState !== 'refreshing'" class="home-notice warn" role="alert" data-testid="home-load-error">
      {{ dataState === 'stale' ? '数据更新失败，以下为上次成功拉取的结果（已过期）' : '数据加载失败，暂无可用数据' }}：{{ providerError }}
      <button type="button" class="ghost-action" @click="reload">重试</button>
    </p>
    <KpiStrip :kpis="kpis" :active="status" :state="dataState" @apply="onKpi" />
    <section class="home-grid" :class="{ narrow, 'drawer-open': drawerOpen }">
      <div class="home-left left-context" :class="{ compact: narrow }">
        <AttentionPanel :metrics="metrics" :state="dataState" :active="status" :compact="narrow" :demo="demo" @apply="applyFilter" />
        <RegionNavPanel v-if="!narrow" :demo="demo" />
      </div>
      <div class="sandbox-stage home-stage" data-testid="spatial-stage">
        <p v-if="noProjects" class="home-empty" data-testid="home-empty">当前账号没有可见的项目或设备。</p>
        <SpatialStage3D v-if="view === '3d' && !webglUnavailable" />
        <SpatialStage2D v-else />
        <div class="map-attribution">开发边界：DataV.GeoAtlas · 仅限内部原型，未经审图<br />公开展示前须替换合规地图并标注来源 / 审图号</div>
      </div>
      <InspectorPanel :demo="demo" :drawer="narrow" />
    </section>
    <section v-if="narrow" class="home-below left-context">
      <div class="home-below-nav"><RegionNavPanel :demo="demo" /></div>
      <InspectorPanel v-if="!selected && !selectedProject" :demo="demo" :drawer="false" />
    </section>
    <EventStrip :demo="demo" :state="dataState" />
    <!-- Independent 15 s Cloud Runtime poll: its own source and refresh cycle,
         kept apart from the homepage numbers above (R1-3). -->
    <CloudRuntime v-if="cloudMode" compact />
    <span v-if="selected && !hasLocation(selected)" class="sr-only">该设备无空间位置</span>
    <span v-if="selectedProject && !hasLocation(selectedProject)" class="sr-only">该项目无空间位置</span>
  </main>
</template>
