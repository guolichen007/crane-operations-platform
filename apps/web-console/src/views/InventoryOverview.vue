<script setup lang="ts">
import { computed, ref, shallowRef, onMounted, onUnmounted, watch } from 'vue';
import type { InventoryProject } from '../../../../contracts/typescript/types.gen';
import { listProjects, listAssets, listDevices } from '../cloud/api';
import { useCloudQuery } from '../cloud/useCloudQuery';
import { inventorySummary, lifecycleName, projectConnection } from '../cloud/inventory';
import { MapSceneManager } from '../three/map/MapSceneManager';
import CloudRuntime from '../components/CloudRuntime.vue';
const { data, error, loading } = useCloudQuery(async () => {
  const [projects, assets, devices] = await Promise.all([
    listProjects(),
    listAssets(),
    listDevices(),
  ]);
  return { projects, assets, devices };
});
const summary = computed(() =>
  data.value
    ? inventorySummary(data.value.projects, data.value.assets, data.value.devices)
    : undefined
);
const metrics = computed(() => [
  ['项目总数', summary.value?.projects],
  ['待实施/预开发项目', summary.value?.planned],
  ['已完成待接入项目', summary.value?.awaiting],
  ['规划行车资产', summary.value?.assets],
  ['模拟在线', summary.value?.simulationOnline],
  ['真实接入', summary.value?.realOnline],
]);
const host = ref<HTMLElement>(),
  selected = shallowRef<InventoryProject>(),
  mapError = ref(''),
  mapLoading = ref(true);
let scene: MapSceneManager | undefined,
  disposed = false;
watch(data, (value) => {
  scene?.setInventoryProjects(value?.projects ?? []);
  if (!value) selected.value = undefined;
});
onMounted(async () => {
  try {
    scene = new MapSceneManager(
      host.value!,
      {
        change: () => {},
        select: () => {},
        project: (id) => {
          selected.value = data.value?.projects.find((p) => p.id === id);
        },
        error: (message) => {
          mapError.value = message;
        },
        stats: () => {},
      },
      true
    );
    if (import.meta.env.DEV)
      (window as unknown as { __spatialDiagnostics: () => unknown }).__spatialDiagnostics = () =>
        scene?.diagnostics();
    await scene.init();
    if (!disposed) scene.setInventoryProjects(data.value?.projects ?? []);
  } catch (e) {
    if (!disposed) mapError.value = String(e);
  } finally {
    if (!disposed) mapLoading.value = false;
  }
});
onUnmounted(() => {
  disposed = true;
  scene?.dispose();
});
</script>
<template>
  <main class="overview inventory-overview">
    <div class="overview-intro">
      <div>
        <div class="eyebrow">PROJECT INVENTORY / 资产与运行来源</div>
        <h1>全国项目与行车资产</h1>
      </div>
      <router-link to="/projects">全部项目 →</router-link>
    </div>
    <p v-if="error" role="status" class="inventory-note">
      {{ error }}；{{ data ? '保留上次结果' : '尚无资产数据' }}
    </p>
    <p v-else-if="loading" class="inventory-note">正在读取 Inventory API…</p>
    <div class="kpi-strip" data-testid="inventory-summary">
      <div v-for="[label, value] in metrics" :key="label">
        <span>{{ label }}</span
        ><strong>{{ value ?? '—' }}</strong>
      </div>
    </div>
    <CloudRuntime :assets="data?.assets ?? []" inventory-mode />
    <div class="sandbox-stage">
      <div
        ref="host"
        class="map-canvas"
        role="img"
        aria-label="项目所在行政区域地图，不代表精确现场坐标"
      ></div>
      <div v-if="mapLoading" class="scene-loading">正在构建空间沙盘</div>
      <div v-if="mapError" class="scene-error">{{ mapError }}</div>
      <p class="inventory-map-note">
        行政区级定位 · 按地图行政区中心显示，非现场精确坐标。<br />金色：规划/预开发 ·
        蓝色：已完成待接入 · 绿色：真实在线
      </p>
      <aside class="inventory-project-list">
        <button
          v-for="p in data?.projects"
          :key="p.id"
          :data-project-id="p.id"
          @click="selected = p"
        >
          <b>{{ p.name }}</b
          ><small>{{ projectConnection(p) }}</small>
        </button>
      </aside>
      <section v-if="selected" class="inventory-selection" data-testid="inventory-selection">
        <button @click="selected = undefined">关闭</button>
        <h2>{{ selected.name }}</h2>
        <p>{{ lifecycleName[selected.lifecycle] }} · {{ projectConnection(selected) }}</p>
        <p>{{ selected.locationLabel || '行政区未配置' }}</p>
        <p>{{ selected.scheduleText }}</p>
        <p>
          资产 {{ selected.assetDeviceCount }} · 模拟源 {{ selected.simulatedCount }} · 真实在线
          {{ selected.realConnectedCount }}
        </p>
        <router-link :to="`/devices?project=${selected.id}`">查看资产档案 →</router-link>
      </section>
    </div>
  </main>
</template>
