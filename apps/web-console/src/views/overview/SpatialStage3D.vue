<script setup lang="ts">
// Three.js spatial stage: a thin adapter between the unchanged MapSceneManager
// and the homepage workspace store. It reports the displayed drill path to the
// store, converges the scene to store.regionTarget one step at a time (each step
// re-planned, so a newer target supersedes an in-flight one), and executes focus
// requests. MapSceneManager is loaded with import() so the 2D view never pulls
// Three.js.
import { onMounted, onUnmounted, ref, watch } from 'vue';
import { storeToRefs } from 'pinia';
import { ChevronRight, ArrowLeft, Play, Pause, Square, RotateCcw, LocateFixed } from 'lucide-vue-next';
import type { MapSceneManager } from '../../three/map/MapSceneManager';
import { useOperations } from '../../stores/operations';
import { sceneProvider } from '../../scene/provider';
import { planRegionTransition, samePath } from '../../scene/region-path';
import { hasLocation } from '../../scene/home-metrics';
import { DemoTourController } from '../../demo/DemoTourController';

const store = useOperations();
const { source, status, selected, selectedProject, visibleProjects, visibleDevices, regionStack, sceneState, cameraPreset, tour } =
  storeToRefs(store);
const isDev = import.meta.env.DEV;
const host = ref<HTMLElement>();
const loading = ref(true);
const error = ref('');
const debug = ref(false);
const stats = ref<Record<string, string | number>>({});
const observation = ref<'operations' | 'cinematic'>('operations');
const countryView = ref<'overview' | 'assets'>('overview');
const sceneMode = ref<'region' | 'fleet' | 'alarm'>('region');
const tourTitle = ref('');
const progress = ref(0);
let scene: MapSceneManager | undefined;
let tourCtl: DemoTourController | undefined;
let alive = true;
const ready = ref(false);

// ---- drill path convergence ----
// While converge() runs, reported paths are its own intermediate steps. Outside
// it, a path change came from the user (map click, tour, Esc) and becomes the
// new target, so it is written to the URL.
let converging = false;
let runToken = 0;
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
function scenePath(): number[] {
  return scene ? scene.drill.stack.slice(1).map((l) => l.adcode) : [];
}
async function converge() {
  const token = ++runToken;
  if (!scene || !ready.value) return;
  converging = true;
  try {
    for (let guard = 0; guard < 24 && alive && token === runToken; guard++) {
      if (scene.drill.busy) {
        await sleep(60);
        continue;
      }
      const current = scenePath();
      const plan = planRegionTransition(current, store.regionTarget);
      if (plan.backTo === null && plan.enter.length === 0) return;
      stopTour();
      if (plan.backTo !== null) await scene.back(plan.backTo);
      else await scene.enter(plan.enter[0]!);
      if (samePath(current, scenePath()) && !scene.drill.busy) {
        // The scene refused the step (unknown/not a child of the current level).
        store.notice = '无法进入该行政区，已停留在当前层级。';
        store.regionTarget = current;
        return;
      }
    }
  } finally {
    if (token === runToken) converging = false;
  }
}
watch(() => [...store.regionTarget], () => void converge());

// ---- focus requests (lists, Inspector, deep links) ----
async function runFocus() {
  const req = store.focusRequest;
  if (!req || !scene || !ready.value) return;
  store.focusRequest = null;
  stopTour();
  if (req.kind === 'device') {
    const d = sceneProvider.devices.value.find((x) => x.deviceId === req.id);
    if (!d) return;
    store.select(d.deviceId);
    if (hasLocation(d)) await scene.focusDevice(d);
  } else {
    const p = sceneProvider.projects.value.find((x) => x.id === req.id);
    if (!p) return;
    store.selectProject(p.id);
    if (hasLocation(p)) await scene.focusProject(p);
  }
}
watch(() => [store.focusRequest?.seq, ready.value], () => void runFocus());

watch([selected, selectedProject], () => scene?.setSelection(selected.value?.deviceId ?? selectedProject.value?.id ?? null));

// ---- detail close / drawer inset (R1-4) ----
// Closing a project/asset detail (Esc, the panel's close button, browser Back)
// returns the camera to the CURRENT region level — never straight to the
// country; only "返回全国" does that. The narrow-width drawer covers the stage's
// right edge, so the camera keeps the focus target in the uncovered part via the
// existing MapSceneManager.setInspectorWidth() (projection offset, no new camera).
const focusPreset = () => !!scene && /^(PROJECT|DEVICE)_/.test(scene.rig.preset);
function restoreRegionView() {
  if (!scene || scene.drill.busy || !focusPreset()) return;
  void scene.back(scene.drill.stack.length - 1);
}
async function applyInset(px: number) {
  if (!scene) return;
  scene.setInspectorWidth(px);
  if (!scene.drill.busy) return;
  // A drill/focus transition was framed with the previous offset: reframe once it settles.
  while (alive && scene.drill.busy) await sleep(80);
  if (alive && store.inspectorInset === px) scene.setInspectorWidth(px);
}
watch(
  () => [store.inspectorInset, store.selectedId, store.selectedProjectId] as const,
  ([inset, sid, pid], old) => {
    if (!scene || !ready.value) return;
    if (!sid && !pid && (old?.[1] || old?.[2])) restoreRegionView();
    if (inset !== old?.[0]) void applyInset(inset);
  }
);
watch([visibleProjects, visibleDevices], () => scene?.setFilter(visibleProjects.value, visibleDevices.value));

// ---- user controls ----
function stopTour() {
  tourCtl?.exit();
  scene?.setTour(false);
}
async function resetCountry() {
  stopTour();
  store.select(null);
  if (store.regionTarget.length) store.navigateRegion([]); // converge() backs out
  else await scene?.back(0); // country level: clear a project/device focus
}
function crumb(index: number) {
  if (index === 0) void resetCountry();
  else store.navigateRegion(regionStack.value.slice(1, index + 1).map((l) => l.adcode));
}
async function switchObservation(mode: 'operations' | 'cinematic') {
  stopTour();
  await scene?.setObservation(mode);
  observation.value = scene?.rig.observation ?? mode;
}
async function switchCountryView(mode: 'overview' | 'assets') {
  stopTour();
  countryView.value = mode;
  await scene?.setCountryView(mode);
}
async function switchSceneMode(mode: 'region' | 'fleet' | 'alarm') {
  stopTour();
  sceneMode.value = mode;
  source.value = 'all';
  if (mode === 'alarm') {
    status.value = 'ALARM';
    await resetCountry();
  } else {
    status.value = 'all';
    await scene?.setCountryView(mode === 'fleet' ? 'assets' : 'overview');
  }
}
function focusFirstLocated() {
  const p = sceneProvider.realProjects.value.find(hasLocation);
  if (p) store.requestFocus('project', p.id);
}
const canFocusProject = () => sceneProvider.realProjects.value.some(hasLocation);
function playTour() {
  if (!tourCtl) return;
  tourCtl.play();
  if (tourCtl.running) scene?.setTour(true);
}
function pauseTour() {
  tourCtl?.pause();
  scene?.setTour(false);
}
function handleKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    // Same rule as the 2D stage: 1st Esc closes the detail (camera back to the
    // current region), 2nd Esc goes up one region level. Never jumps to the country.
    stopTour();
    if (store.selectedId || store.selectedProjectId) store.select(null); // watcher restores the region view
    else if (focusPreset()) restoreRegionView();
    else if (regionStack.value.length > 1) crumb(regionStack.value.length - 2);
  }
  if (isDev && e.ctrlKey && e.shiftKey && e.key.toLowerCase() === 'd') {
    e.preventDefault();
    debug.value = !debug.value;
  }
}
function userStop(e: PointerEvent) {
  if (tour.value !== 'stopped' && !(e.target as HTMLElement).closest('.tour-controls')) stopTour();
}

onMounted(async () => {
  // Data loading is owned by Overview (single owner): a second ensureLoaded()
  // here would silently retry a failed first load and hide the failure (R1-1).
  window.addEventListener('keydown', handleKey);
  window.addEventListener('start-tour', playTour);
  document.addEventListener('pointerdown', userStop, true);
  try {
    const { MapSceneManager } = await import('../../three/map/MapSceneManager');
    if (!alive || !host.value) return;
    scene = new MapSceneManager(host.value, {
      change: (stack, children, state) => {
        store.regionStack = stack;
        store.regionChildren = children;
        store.sceneState = state;
        store.cameraPreset = scene?.rig.preset ?? 'COUNTRY_OVERVIEW';
        observation.value = scene?.rig.observation ?? 'operations';
        countryView.value = scene?.rig.countryMode ?? 'overview';
        if (/^DRILLING/.test(state)) return;
        const path = stack.slice(1).map((l) => l.adcode);
        store.regionPath = path;
        // Before init completes the scene only reports its initial country state;
        // that must not overwrite a deep-link target the URL already set.
        if (ready.value && !converging && !samePath(path, store.regionTarget)) store.regionTarget = path;
      },
      select: (id) => store.select(id),
      project: (id) => store.selectProject(id),
      error: (m) => (error.value = m),
      stats: (s) => (stats.value = s),
    });
    if (import.meta.env.DEV)
      (window as unknown as { __spatialDiagnostics: () => unknown }).__spatialDiagnostics = () => scene?.diagnostics();
    (window as unknown as { __perfRecorder: () => { start: () => void; stop: () => unknown } }).__perfRecorder = () => ({
      start: () => scene?.startPerfRecording(),
      stop: () => scene?.stopPerfRecording(),
    });
    // R1-5: a map resource failure is recoverable (retry button), and a lost
    // WebGL context that is not restored in time falls back to the 2D view —
    // the stage must never stay blank.
    scene.canvas.addEventListener('webglcontextlost', onContextLost, false);
    scene.canvas.addEventListener('webglcontextrestored', onContextRestored, false);
    if (!(await scene.init())) {
      resourceFailed.value = true;
      return;
    }
    if (!alive || !document.contains(host.value!)) return;
    scene.setFilter(visibleProjects.value, visibleDevices.value);
    if (store.inspectorInset) scene.setInspectorWidth(store.inspectorInset);
    ready.value = true;
    void converge();
  } catch (e) {
    // WebGL unavailable: the page falls back to the 2D view (Overview watches this).
    store.webglUnavailable = `WebGL 初始化失败：${String(e)}`;
  } finally {
    loading.value = false;
  }
  if (sceneProvider.mode === 'demo' && scene) tourCtl = createTour();
});
onUnmounted(() => {
  alive = false;
  clearTimeout(lostTimer);
  scene?.canvas.removeEventListener('webglcontextlost', onContextLost, false);
  scene?.canvas.removeEventListener('webglcontextrestored', onContextRestored, false);
  runToken++;
  window.removeEventListener('keydown', handleKey);
  window.removeEventListener('start-tour', playTour);
  document.removeEventListener('pointerdown', userStop, true);
  tourCtl?.dispose();
  scene?.dispose();
  tour.value = 'stopped';
});

// ---- recoverability (R1-5) ----
const resourceFailed = ref(false);
const contextLost = ref(false);
const CONTEXT_RESTORE_MS = 3000;
let lostTimer: ReturnType<typeof setTimeout> | undefined;
function onContextLost(e: Event) {
  e.preventDefault(); // allow the browser to restore the context
  contextLost.value = true;
  clearTimeout(lostTimer);
  lostTimer = setTimeout(() => {
    if (alive && contextLost.value) store.webglUnavailable = 'WebGL 上下文丢失且未恢复';
  }, CONTEXT_RESTORE_MS);
}
function onContextRestored() {
  clearTimeout(lostTimer);
  contextLost.value = false;
  scene?.redraw();
}
async function retryResources() {
  if (!scene) return;
  resourceFailed.value = false;
  error.value = '';
  loading.value = true;
  try {
    if (!(await scene.init())) {
      resourceFailed.value = true;
      return;
    }
    if (!alive) return;
    scene.setFilter(visibleProjects.value, visibleDevices.value);
    if (store.inspectorInset) scene.setInspectorWidth(store.inspectorInset);
    ready.value = true;
    void converge();
    if (sceneProvider.mode === 'demo' && !tourCtl) tourCtl = createTour();
  } finally {
    loading.value = false;
  }
}
function useLightView() {
  store.view = '2d';
}

function createTour() {
  const real = () => sceneProvider.realProjects.value;
  return new DemoTourController(
    [
      { at: 0, title: '全国设备网络 · 资产全景', action: async () => { source.value = 'all'; status.value = 'all'; store.select(null); await scene?.back(0); if (tourCtl?.running) scene?.setTour(true); } },
      { at: 4, title: '6 个匿名登记项目 · 10 台计划设备', action: () => { source.value = 'real'; } },
      { at: 8, title: '浙江省 · 近期实施双项目', action: async () => { await scene?.enter(330000); if (tourCtl?.running) scene?.setTour(true); } },
      { at: 14, title: '嘉兴 → 海宁 · 逐级查看', action: async () => { await scene?.enter(330400); if (tourCtl?.running) await scene?.enter(330481); if (tourCtl?.running) scene?.setTour(true); } },
      { at: 20, title: 'Project-A · 2026 年 11 月实施', action: () => { const p = real()[0]; if (p) store.selectProject(p.id); } },
      { at: 27, title: '回到全国 · 项目实施态势', action: async () => { store.select(null); await scene?.back(0); if (tourCtl?.running) scene?.setTour(true); } },
      { at: 32, title: 'Project-F · 3 台行车与两阶段里程碑', action: async () => { const p = real()[5]; if (p) await scene?.focusProject(p, () => !!tourCtl?.running); if (tourCtl?.running) scene?.setTour(true); } },
      { at: 38, title: '全国设备网络 · 持续连接', action: async () => { source.value = 'all'; store.select(null); await scene?.back(0); if (tourCtl?.running) scene?.setTour(true); } },
    ],
    (r, p, title, value) => {
      tour.value = r ? (p ? 'paused' : 'playing') : 'stopped';
      tourTitle.value = title;
      progress.value = value;
    }
  );
}
defineExpose({ playTour });
</script>
<template>
  <div class="map-canvas" ref="host" role="img" aria-label="全国行车三维地图，支持鼠标拖动、缩放和点击行政区"></div>
  <div v-if="loading" class="scene-loading"><span></span>正在构建空间沙盘</div>
  <div v-if="resourceFailed" class="scene-error scene-recover" role="alert" data-testid="scene-resource-error">
    地图资源加载失败：{{ error || '未知错误' }}
    <button type="button" @click="retryResources">重试</button>
    <button type="button" @click="useLightView">切换轻量视图</button>
  </div>
  <div v-else-if="contextLost" class="scene-error scene-recover" role="alert" data-testid="scene-context-lost">
    三维渲染上下文已丢失，正在等待恢复…
    <button type="button" @click="useLightView">切换轻量视图</button>
  </div>
  <div v-else-if="error" class="scene-error" role="alert">{{ error }}<button @click="error = ''">关闭</button></div>
  <div class="map-topline">
    <nav class="breadcrumb" aria-label="行政区路径">
      <button v-for="(s, i) in regionStack" :key="s.adcode" @click="crumb(i)">
        {{ s.name }}<ChevronRight v-if="i < regionStack.length - 1" :size="12" />
      </button>
      <span v-if="selectedProject || selected" class="context-crumb">› {{ selectedProject?.projectName ?? selected?.projectName }}</span>
    </nav>
    <button v-if="regionStack.length > 1 || sceneState === 'PROJECT_FOCUS'" class="back-button" @click="resetCountry()">
      <ArrowLeft :size="12" />返回全国
    </button>
  </div>
  <div v-if="cameraPreset.startsWith('COUNTRY_')" class="map-view-switch">
    <button :class="{ selected: countryView === 'overview' }" @click="switchCountryView('overview')">全国态势</button
    ><button :class="{ selected: countryView === 'assets' }" @click="switchCountryView('assets')">设备聚焦</button>
  </div>
  <div class="observation-switch" aria-label="观察角度">
    <button :class="{ selected: observation === 'operations' }" @click="switchObservation('operations')">运营视角</button>
    <button :class="{ selected: observation === 'cinematic' }" @click="switchObservation('cinematic')">立体视角</button>
  </div>
  <div v-if="cameraPreset.startsWith('COUNTRY_')" class="scene-mode-dock" aria-label="场景模式">
    <button :class="{ selected: sceneMode === 'region' }" @click="switchSceneMode('region')">行政区</button>
    <button :class="{ selected: sceneMode === 'fleet' }" @click="switchSceneMode('fleet')">资产</button>
    <button :class="{ selected: sceneMode === 'alarm' }" @click="switchSceneMode('alarm')">告警</button>
  </div>
  <div v-if="cameraPreset.startsWith('COUNTRY_')" class="south-sea-inset-note">南海诸岛 · 附图</div>
  <div class="map-tools">
    <button title="重置全国视角" aria-label="重置全国视角" @click="resetCountry()"><RotateCcw :size="15" /></button
    ><button
      title="聚焦首个有空间位置的登记项目"
      aria-label="聚焦首个登记项目"
      :disabled="!canFocusProject()"
      @click="focusFirstLocated"
    >
      <LocateFixed :size="16" />
    </button>
  </div>
  <div v-if="tour !== 'stopped'" class="tour-controls">
    <div><Play :size="14" /><span>{{ tourTitle }}</span><b>{{ Math.floor(progress * 45) }} / 45s</b></div>
    <div class="tour-progress"><i :style="{ width: progress * 100 + '%' }"></i></div>
    <button v-if="tour === 'playing'" aria-label="暂停巡演" @click="pauseTour"><Pause :size="14" />暂停</button
    ><button v-else @click="playTour"><Play :size="14" />继续</button
    ><button aria-label="退出巡演" @click="stopTour"><Square :size="12" />退出巡演</button>
  </div>
  <div v-if="isDev && debug" class="debug-panel" data-testid="debug-panel">
    <b>RENDER DIAGNOSTICS</b>
    <div v-for="(value, key) in stats" :key="key"><span>{{ key }}</span><strong>{{ value }}</strong></div>
  </div>
</template>
