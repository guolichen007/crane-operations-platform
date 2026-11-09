<script setup lang="ts">
// Operations Center homepage (H1 → H2-1D). Layout shell only: business state lives in the
// operations store, URL sync in useHomeUrlSync, rendering in SpatialStage3D (unchanged
// MapSceneManager, dynamic import) or SpatialStage2D (SVG).
// Numbers (H2-1D): API mode → the authoritative operations summary (scene/use-operations-summary.ts,
// scene/home-summary.ts) for the displayed region; the map keeps Inventory + Shadow (sceneProvider)
// for spatial display only. Demo mode → local fixtures via scene/home-metrics.ts, never the API.
import { computed, defineAsyncComponent, onMounted, onUnmounted, ref, watch } from 'vue';
import './overview/home.css';
import { storeToRefs } from 'pinia';
import { RefreshCw, X } from 'lucide-vue-next';
import { useRoute, useRouter } from 'vue-router';
import { useOperations } from '../stores/operations';
import { sceneProvider } from '../scene/provider';
import { homeMetrics, homeKpis, hasLocation, homeDataState, hasNumbers, scopeLabel, demoAttention, type HomeDataState } from '../scene/home-metrics';
import { summaryKpis, summaryAttention, WINDOW_LABEL } from '../scene/home-summary';
import { useOperationsSummary, summaryController } from '../scene/use-operations-summary';
import { formatCloudTime } from '../cloud/format';
import { cloudMode, type AlarmWindow } from '../cloud/api';
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

// ---- Map data (both modes): Inventory + Shadow for spatial display. Its own state.
const mapState = computed<HomeDataState>(() =>
  homeDataState({ mode: sceneProvider.mode, loading: sceneProvider.loading.value, error: sceneProvider.error.value, fetchedAt: sceneProvider.fetchedAt.value })
);

// ---- Numbers.
// API: the summary for the DISPLAYED region (leaf of the drill path; country = null). The region is
// requested from the server — never derived by filtering the assets the map happens to hold.
const alarmWindow = ref<AlarmWindow>('PT24H');
const summaryRegion = computed(() => store.regionPath[store.regionPath.length - 1] ?? null);
const snap = demo ? null : useOperationsSummary(() => summaryRegion.value, () => alarmWindow.value);
const summary = computed(() => snap?.value.data ?? null);
// R1-1 state vocabulary, now driven by the summary in API mode ('idle' = no login identity yet).
watch(
  () => (snap ? (snap.value.state === 'idle' ? 'loading' : snap.value.state) : mapState.value),
  (s) => (dataState.value = s),
  { immediate: true }
);
// Server-provided review asset ids drive the PENDING_REVIEW map filter (≤100, never recomputed).
watch(summary, (s) => (store.reviewIds = new Set(s?.metrics.real.pendingReviewAssets.map((a) => a.id) ?? [])), { immediate: true });

// Demo: KPI / Attention base set = source ∩ displayed region over local fixtures (H1-R1-2).
const metrics = computed(() => (demo && hasNumbers(dataState.value) ? homeMetrics(store.scopedProjects, store.scopedDevices) : null));
const plannedDevices = computed(() => store.scopedProjects.filter((p) => p.dataSource === 'real').reduce((s, p) => s + p.quantity, 0));
const kpis = computed(() => (demo ? homeKpis('demo', metrics.value, null, plannedDevices.value) : summaryKpis(summary.value)));
const attentionRows = computed(() => (demo ? demoAttention(metrics.value) : summaryAttention(summary.value)));
const scopeText = computed(() =>
  demo ? scopeLabel('demo', store.scope, store.scopeName) : `全部授权项目 · ${store.regionPath.length ? store.scopeName : '全国'}`
);

// ---- Time semantics (H2-1D-5): server sampling / server generation / browser receive, all Shanghai time.
const times = computed(() => {
  const s = summary.value;
  return s ? { sampled: formatCloudTime(s.sampledAt), generated: formatCloudTime(s.generatedAt), fetched: formatCloudTime(snap!.value.fetchedAt) } : null;
});
const asOf = computed(() => {
  if (demo) return '本地演示数据 · 非遥测';
  const t = times.value;
  switch (dataState.value) {
    case 'loading':
      return '正在获取运营统计…';
    case 'unavailable':
      return '运营统计不可用 · 暂无可用数据';
    case 'refreshing':
      return `统计取样 ${t!.sampled} · 正在更新…`;
    case 'stale':
      return `统计取样 ${t!.sampled} · 更新失败，数据已过期`;
    default:
      return `统计取样 ${t!.sampled} · 每 60 秒更新`;
  }
});
const asOfTitle = computed(() =>
  times.value
    ? `统计取样 sampledAt ${times.value.sampled}（服务端单一快照）\n生成完成 generatedAt ${times.value.generated}（服务端）\n浏览器接收 ${times.value.fetched}\n均为上海时间；不是设备遥测的观测时间`
    : '服务端运营统计（GET /api/v1/operations/summary）'
);
const loadError = computed(() => (demo ? sceneProvider.error.value : snap!.value.error));
const showError = computed(() => !!loadError.value && (dataState.value === 'stale' || dataState.value === 'unavailable'));
const mapError = computed(() => !demo && mapState.value !== 'refreshing' && !!sceneProvider.error.value);
// "No projects" = an authoritative 0 (summary ready) — or, in demo, an empty fixture.
const noProjects = computed(() =>
  demo ? dataState.value === 'ready' && sceneProvider.projects.value.length === 0 : dataState.value === 'ready' && summary.value?.scope.authorizedProjects === 0
);
function reload() {
  if (!demo) void summaryController().refresh();
  sceneProvider.refresh().catch(() => undefined);
}
function reloadMap() {
  sceneProvider.refresh().catch(() => undefined);
}

/**
 * KPI click (H2-1D-4). Only "待复核" has an exact server-side detail (pendingReviewAssets) and filters
 * the map/Inspector to those ids. Every other API card opens the Inventory asset list for the same
 * authorization + region; for REAL 在线 / REAL 告警 / 无运行身份 that list is NOT the exact KPI set and
 * says so (?kpi=), because no list endpoint shares the summary predicate yet.
 */
function onKpi(filter: string, id: string) {
  if (!demo && filter === 'all') {
    const region = store.regionPath[store.regionPath.length - 1];
    const query: Record<string, string> = region ? { region: String(region) } : {};
    if (id !== 'registered') query.kpi = id;
    void router.push({ path: '/devices', query });
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
  // The page owns the map data load (not the 3D stage): the 2D view and the WebGL fallback need it too.
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
        :title="asOfTitle"
      >{{ asOf }}</span>
      <button
        v-if="!demo"
        type="button"
        class="home-refresh"
        data-testid="home-refresh"
        :disabled="dataState === 'loading' || dataState === 'refreshing'"
        aria-label="立即更新运营统计"
        title="立即更新运营统计（另每 60 秒自动更新）"
        @click="reload"
      ><RefreshCw :size="13" /></button>
      <span class="home-scope" data-testid="kpi-scope" title="KPI 与「需要关注」的统计范围；状态过滤只影响地图与列表展示，不改变指标">
        统计范围 <b>{{ scopeText }}</b><i v-if="dataState === 'stale'" class="home-stale-tag" data-testid="kpi-stale">已过期</i>
      </span>
      <label v-if="!demo" class="home-window" title="历史告警记录的统计时间窗">
        <span class="sr-only">告警时间窗</span>
        <select v-model="alarmWindow" data-testid="alarm-window" aria-label="告警时间窗">
          <option v-for="(label, w) in WINDOW_LABEL" :key="w" :value="w">{{ label }}</option>
        </select>
      </label>
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
    <p v-if="showError" class="home-notice warn" role="alert" data-testid="home-load-error" :data-status="demo ? '' : snap?.status ?? ''">
      {{ dataState === 'stale' ? '统计更新失败，以下为当前范围上次成功获取的结果（已过期）' : '运营统计加载失败，暂无可用数据' }}：{{ loadError }}
      <button type="button" class="ghost-action" @click="reload">重试</button>
    </p>
    <p v-if="mapError" class="home-notice warn" role="status" data-testid="map-load-error">
      地图设备数据（Inventory / Shadow）加载失败，地图标注可能不完整；指标不受影响：{{ sceneProvider.error.value }}
      <button type="button" class="ghost-action" @click="reloadMap">重试地图数据</button>
    </p>
    <KpiStrip :kpis="kpis" :active="status" :state="dataState" @apply="onKpi" />
    <section class="home-grid" :class="{ narrow, 'drawer-open': drawerOpen }">
      <div class="home-left left-context" :class="{ compact: narrow }">
        <AttentionPanel :rows="attentionRows" :state="dataState" :active="status" :compact="narrow" :demo="demo"
          :note="demo ? undefined : '点击「待复核且告警」在地图中显示服务端返回的复核资产；其余为独立统计，不相加。'" @apply="applyFilter" />
        <RegionNavPanel v-if="!narrow" :demo="demo" :summary="summary" :state="dataState" />
      </div>
      <div class="sandbox-stage home-stage" data-testid="spatial-stage">
        <p v-if="noProjects" class="home-empty" data-testid="home-empty">当前账号没有已授权的项目，统计为 0。</p>
        <SpatialStage3D v-if="view === '3d' && !webglUnavailable" />
        <SpatialStage2D v-else />
        <div class="map-attribution">开发边界：DataV.GeoAtlas · 仅限内部原型，未经审图<br />公开展示前须替换合规地图并标注来源 / 审图号</div>
      </div>
      <InspectorPanel :demo="demo" :drawer="narrow" :summary="summary" />
    </section>
    <section v-if="narrow" class="home-below left-context">
      <div class="home-below-nav"><RegionNavPanel :demo="demo" :summary="summary" :state="dataState" /></div>
      <InspectorPanel v-if="!selected && !selectedProject" :demo="demo" :drawer="false" :summary="summary" />
    </section>
    <EventStrip :demo="demo" :state="mapState" :summary="summary" />
    <!-- Independent 15 s Cloud Runtime poll: its own source and refresh cycle,
         kept apart from the homepage numbers above (R1-3). -->
    <CloudRuntime v-if="cloudMode" compact />
    <span v-if="selected && !hasLocation(selected)" class="sr-only">该设备无空间位置</span>
    <span v-if="selectedProject && !hasLocation(selectedProject)" class="sr-only">该项目无空间位置</span>
  </main>
</template>
