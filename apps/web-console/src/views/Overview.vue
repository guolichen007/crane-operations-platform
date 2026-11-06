<script setup lang="ts">
// Operations Center homepage (H1). Layout shell only: business state lives in the
// operations store, URL sync in useHomeUrlSync, numbers in scene/home-metrics,
// rendering in SpatialStage3D (unchanged MapSceneManager, dynamic import) or
// SpatialStage2D (SVG). See docs/04-前端与可视化/Homepage-H1-0-高保真布局设计.md.
import { computed, defineAsyncComponent, onMounted, onUnmounted, ref, watch } from 'vue';
import './overview/home.css';
import { storeToRefs } from 'pinia';
import { X } from 'lucide-vue-next';
import { useRoute } from 'vue-router';
import { useOperations } from '../stores/operations';
import { sceneProvider } from '../scene/provider';
import { homeMetrics, homeKpis, hasLocation } from '../scene/home-metrics';
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
const { source, status, view, webglUnavailable, notice, regionStack, sceneState, cameraPreset, tour, selected, selectedProject } =
  storeToRefs(store);
const demo = sceneProvider.mode === 'demo';
useHomeUrlSync();

const metrics = computed(() =>
  sceneProvider.loading.value && !sceneProvider.devices.value.length
    ? null
    : homeMetrics(sceneProvider.projects.value, sceneProvider.devices.value)
);
const plannedDevices = computed(() => sceneProvider.realProjects.value.reduce((s, p) => s + p.quantity, 0));
const kpis = computed(() => homeKpis(demo ? 'demo' : 'api', metrics.value, sceneProvider.alarmRecordCount.value, plannedDevices.value));
const asOf = computed(() => (demo ? '本地演示数据' : sceneProvider.loadedAt.value ? `数据截至 ${formatCloudTime(sceneProvider.loadedAt.value)}` : '正在读取 Cloud API'));
const providerError = computed(() => sceneProvider.error.value);
const noProjects = computed(() => !sceneProvider.loading.value && !providerError.value && sceneProvider.projects.value.length === 0);

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
  onMq();
  mq.addEventListener('change', onMq);
  if (import.meta.env.DEV) (window as unknown as { __routeQuery?: () => unknown }).__routeQuery = () => route.query;
});
onUnmounted(() => mq.removeEventListener('change', onMq));
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
      <span class="home-asof" data-testid="home-asof">{{ asOf }}</span>
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
    <p v-if="providerError" class="home-notice warn" role="alert" data-testid="home-load-error">
      数据加载失败：{{ providerError }} <button type="button" class="ghost-action" @click="sceneProvider.refresh().catch(() => undefined)">重试</button>
    </p>
    <CloudRuntime v-if="cloudMode" compact />
    <KpiStrip :kpis="kpis" :active="status" :loading="!metrics" @apply="applyFilter" />
    <section class="home-grid" :class="{ narrow, 'drawer-open': narrow && (!!selected || !!selectedProject) }">
      <div class="home-left left-context" :class="{ compact: narrow }">
        <AttentionPanel :metrics="metrics" :active="status" :compact="narrow" :demo="demo" @apply="applyFilter" />
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
    <EventStrip :demo="demo" />
    <span v-if="selected && !hasLocation(selected)" class="sr-only">该设备无空间位置</span>
    <span v-if="selectedProject && !hasLocation(selectedProject)" class="sr-only">该项目无空间位置</span>
  </main>
</template>
