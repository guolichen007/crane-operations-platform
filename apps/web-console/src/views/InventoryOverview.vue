<script setup lang="ts">
import { computed, ref, shallowRef, onMounted, onUnmounted, watch } from 'vue';
import { useRoute } from 'vue-router';
import { ArrowUpRight, ChevronRight, RotateCcw, MapPin, Layers3, Radio, Box, Orbit } from 'lucide-vue-next';
import type { InventoryProject } from '../../../../contracts/typescript/types.gen';
import { listProjects, listAssets, listDevices, listAlarms } from '../cloud/api';
import { useCloudQuery } from '../cloud/useCloudQuery';
import { inventorySummary, lifecycleName, projectConnection } from '../cloud/inventory';
import type { MapSceneManager } from '../three/map/MapSceneManager';
import { countryLevel, type MapLevel } from '../three/map/DrilldownController';
import type { RegionEntry } from '../three/map/GeoJsonLoader';
import GlassPanel from '../components/GlassPanel.vue';
import MetricCard from '../components/MetricCard.vue';
import SkeletonPanel from '../components/SkeletonPanel.vue';
import AlarmTicker from '../components/AlarmTicker.vue';
import StatusPill from '../components/StatusPill.vue';
const route = useRoute();
const { data, error, loading } = useCloudQuery(async () => {
  const [projects, assets, devices, alarms] = await Promise.all([listProjects(), listAssets(), listDevices(), listAlarms()]);
  return { projects, assets, devices, alarms };
});
const summary = computed(() => data.value ? inventorySummary(data.value.projects, data.value.assets, data.value.devices) : undefined);
const metrics = computed(() => [
  { label: '全国项目', value: summary.value?.projects, note: '授权范围内项目' },
  { label: '规划 / 预开发', value: summary.value?.planned, note: '项目生命周期' },
  { label: '已完成待接入', value: summary.value?.awaiting, note: '档案与接入分别管理' },
  { label: '行车资产', value: summary.value?.assets, note: '业务资产总量' },
  { label: '模拟源在线', value: summary.value?.simulationOnline, note: 'SIMULATION · 非现场' },
  { label: '真实源在线', value: summary.value?.realOnline, note: 'REAL CONNECTED' },
]);
const host = ref<HTMLElement>(), selected = shallowRef<InventoryProject>(), mapError = ref(''), mapLoading = ref(true);
const stack = ref<MapLevel[]>([countryLevel]), children = ref<RegionEntry[]>([]), search = ref(''), observation = ref<'operations' | 'cinematic'>('operations');
const projects = computed(() => (data.value?.projects ?? []).filter(p => (p.name + p.id).toLowerCase().includes(search.value.toLowerCase())));
let scene: MapSceneManager | undefined, disposed = false;
watch(data, value => { scene?.setInventoryProjects(value?.projects ?? []); if (!value) selected.value = undefined; });
watch(selected, p => { scene?.setInspectorWidth(p ? 340 : 0); });
async function reset() { selected.value = undefined; await scene?.back(0); }
function changeObservation() { observation.value = observation.value === 'operations' ? 'cinematic' : 'operations'; if (scene) { scene.rig.observation = observation.value; void scene.back(0); } }
function selectProject(p: InventoryProject) { selected.value = p; if (p.adminCode && scene) void scene.enter(p.adminCode); }
onMounted(async () => {
  try {
    const { MapSceneManager: Manager } = await import('../three/map/MapSceneManager');
    if (disposed) return;
    scene = new Manager(host.value!, {
      change: (levels, entries) => { stack.value = levels; children.value = entries; },
      select: () => {}, project: id => { selected.value = data.value?.projects.find(p => p.id === id); },
      error: () => { mapError.value = '空间底图暂不可用，仍可通过项目列表访问资产。'; }, stats: () => {},
    }, true);
    if (import.meta.env.DEV) (window as unknown as { __spatialDiagnostics: () => unknown }).__spatialDiagnostics = () => scene?.diagnostics();
    await scene.init();
    if (!disposed) { scene.setInventoryProjects(data.value?.projects ?? []); const p = data.value?.projects.find(p => p.id === route.query.project); if (p) selectProject(p); }
  } catch { if (!disposed) mapError.value = '当前环境未启用 WebGL · 已切换二维资产视图'; }
  finally { if (!disposed) mapLoading.value = false; }
});
onUnmounted(() => { disposed = true; scene?.dispose(); });
</script>
<template>
  <main class="premium-overview inventory-overview">
    <div class="premium-page-heading"><div><p class="premium-eyebrow">NATIONAL OPERATIONS / 全国运营态势</p><h1>全国运营中枢 <span>资产可见 · 运行可知</span></h1></div><div class="data-origin"><i></i> CLOUD API <span>/</span> 行政区级定位</div></div>
    <p v-if="error" role="status" class="premium-query-error">{{ error }}；{{ data ? '保留上次结果' : '尚无资产数据' }}</p>
    <div class="premium-kpis" data-testid="inventory-summary"><MetricCard v-for="m in metrics" :key="m.label" v-bind="m" /></div>
    <div class="premium-sandbox sandbox-stage">
      <div ref="host" class="map-canvas" role="img" aria-label="全国项目行政区级空间沙盘，非现场精确坐标"></div>
      <div class="scene-topline"><span><i></i> SPATIAL ASSET NETWORK</span><small>CHINA / {{ stack[stack.length - 1]?.name }}</small></div>
      <SkeletonPanel v-if="mapLoading" class="scene-loading" label="正在构建全国空间沙盘" />
      <div v-if="mapError" class="map-fallback" data-testid="webgl-fallback"><Layers3 :size="52" /><b>全国资产空间视图</b><p>{{ mapError }}</p><div class="fallback-blueprint"></div></div>
      <div class="scene-hud">
        <aside class="hud-left">
          <GlassPanel title="资产视图" subtitle="ASSET DIRECTORY">
            <div class="asset-summary"><div><strong>{{ summary?.projects ?? '—' }}</strong><span>授权项目</span></div><div><strong>{{ summary?.assets ?? '—' }}</strong><span>行车资产</span></div></div>
            <label class="premium-search"><input v-model="search" placeholder="搜索项目名称 / 编号" aria-label="搜索项目" /></label>
            <div class="hud-projects"><button v-for="p in projects" :key="p.id" :data-project-id="p.id" :class="{ selected: selected?.id === p.id }" @click="selectProject(p)"><span class="project-symbol"><Box :size="16" /></span><div><b>{{ p.name }}</b><small>{{ p.id }} · {{ lifecycleName[p.lifecycle] }}</small></div><span class="project-quantity">{{ p.assetDeviceCount }}<small> 台</small></span><ChevronRight :size="12" /></button></div>
            <p v-if="!projects.length && !loading" class="no-data">暂无授权项目</p>
            <p class="hud-note"><MapPin :size="12" /> 行政区中心示意，非现场坐标</p>
          </GlassPanel>
          <div class="region-directory"><select aria-label="行政区下钻" value="" @change="scene?.enter(Number(($event.target as HTMLSelectElement).value))"><option value="" disabled>选择行政区域下钻</option><option v-for="c in children" :key="c.adcode" :value="c.adcode">{{ c.name }}</option></select></div>
        </aside>
        <aside class="hud-right">
          <GlassPanel v-if="selected" title="项目上下文" class="inventory-selection" data-testid="inventory-selection"><template #action><button @click="selected = undefined">关闭</button></template><p class="premium-eyebrow">{{ selected.id }}</p><h2 class="inspector-name">{{ selected.name }}</h2><StatusPill :label="lifecycleName[selected.lifecycle]" /><p class="inspector-copy">{{ lifecycleName[selected.lifecycle] }} · {{ projectConnection(selected) }}</p><p class="inspector-copy">{{ selected.locationLabel || '行政区未配置' }}</p><p class="inspector-copy">{{ selected.scheduleText }}</p><p class="inspector-copy">资产 {{ selected.assetDeviceCount }} · 模拟源 {{ selected.simulatedCount }} · 真实在线 {{ selected.realConnectedCount }}</p><RouterLink class="premium-link" :to="'/devices?project=' + selected.id">查看项目设备 <ArrowUpRight :size="14" /></RouterLink></GlassPanel>
          <GlassPanel v-else title="最近告警" subtitle="ALARM RECORDS"><template #action><RouterLink to="/alarms"><ArrowUpRight :size="14" /></RouterLink></template><AlarmTicker :alarms="data?.alarms ?? []" /><p class="hud-note">最近最多 100 条记录 · 不代表未处理数量</p></GlassPanel>
          <GlassPanel title="运行来源" subtitle="SOURCE DISTRIBUTION"><div class="source-rings"><div class="source-ring"><strong>{{ summary?.assets ?? '—' }}</strong><small>行车资产</small></div><div class="source-legend"><p><i></i> 模拟在线 <b>{{ summary?.simulationOnline ?? '—' }}</b></p><p><i></i> 真实在线 <b>{{ summary?.realOnline ?? '—' }}</b></p><p><i></i> 其余资产 <b>{{ summary ? summary.assets - summary.simulationOnline - summary.realOnline : '—' }}</b></p></div></div><p class="hud-note">模拟运行不代表真实现场在线</p></GlassPanel>
        </aside>
      </div>
      <div class="scene-footer"><div class="scene-breadcrumb"><button v-for="(s, i) in stack" :key="s.adcode" @click="scene?.back(i)">{{ s.name }} <ChevronRight v-if="i < stack.length - 1" :size="11" /></button></div><div class="scene-controls"><button @click="reset"><RotateCcw :size="13" />全国 / 复位</button><button :aria-pressed="observation === 'cinematic'" @click="changeObservation"><Orbit :size="13" />{{ observation === 'cinematic' ? '立体视角' : '运营视角' }}</button></div></div>
    </div>
    <footer class="premium-statusbar" data-testid="cloud-runtime"><span><Radio :size="12" /> 资产与运行来源独立</span><p>模拟运行源在线 {{ summary?.simulationOnline ?? '—' }} · 真实运行源在线 {{ summary?.realOnline ?? '—' }}<span> · SIM 为模拟设备，不能代表现场在线</span></p><small>U-DAO / A1.1 PREMIUM</small></footer>
  </main>
</template>
