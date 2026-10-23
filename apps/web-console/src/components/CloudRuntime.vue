<script setup lang="ts">
import { computed } from 'vue';
import type { AssetDevice } from '../../../../contracts/typescript/types.gen';
const props = defineProps<{ assets?: AssetDevice[]; inventoryMode?: boolean }>();
import { listDevices, listAlarms } from '../cloud/api';
import { summarizeDevices } from '../cloud/format';
import { useCloudQuery } from '../cloud/useCloudQuery';
const { data, error, loading, refresh } = useCloudQuery(async () => {
  const [devices, alarms] = await Promise.all([listDevices(), listAlarms()]);
  return { ...summarizeDevices(devices), devices, alarms: alarms.length };
});
const metrics = computed(() => [
  [props.inventoryMode ? 'Runtime连接' : 'Cloud接入设备', data.value?.total, '台'],
  [props.inventoryMode ? 'Runtime MQTT在线（含模拟）' : 'MQTT在线', data.value?.online, '台'],
  ['实时查看', data.value?.live, '台设备'],
  ['当前告警记录', data.value?.alarms, '条'],
]);
const sources = computed(() => {
  const online = new Set(
    data.value?.devices
      .filter((d) => d.presence.connectivity === 'MQTT_CONNECTED')
      .map((d) => d.deviceId)
  );
  const assets = props.assets ?? [];
  return {
    simulation: assets.filter(
      (a) => a.runtimeSource === 'SIMULATION' && a.runtimeDeviceId && online.has(a.runtimeDeviceId)
    ).length,
    real: assets.filter(
      (a) => a.runtimeSource === 'REAL' && a.runtimeDeviceId && online.has(a.runtimeDeviceId)
    ).length,
  };
});
</script>
<template>
  <section class="cloud-runtime" aria-label="Cloud实际运行状态" data-testid="cloud-runtime">
    <div class="runtime-title">
      <strong>CLOUD RUNTIME · Cloud API</strong><router-link to="/devices">云端设备 →</router-link>
    </div>
    <div class="runtime-metrics">
      <div v-for="[label, value, unit] in metrics" :key="label">
        <span>{{ label }}</span
        ><b>{{ value ?? '—' }}</b
        ><small>{{ unit }}</small>
      </div>
    </div>
    <p v-if="error" role="status">
      {{ error }}；{{ data ? '保留上次结果，当前状态待刷新' : '尚无云端统计' }}
      <button @click="refresh">重试</button>
    </p>
    <p v-else-if="loading">正在读取 Cloud API…</p>
    <p v-else-if="!inventoryMode">
      {{ data?.total }}台云设备已接入，位置元数据未配置；SIM 为模拟设备。实时查看按上游 LIVE
      的设备数统计；告警为 API 最近最多100条记录，不代表未处理数量。
    </p>
    <p v-if="inventoryMode">
      模拟运行源在线 {{ sources.simulation }} · 真实运行源在线 {{ sources.real }}；SIM
      为模拟设备，不能代表现场在线。实时查看按上游LIVE设备计数；告警为最近最多100条记录，不代表未处理数量。
    </p>
    <p v-if="inventoryMode">
      项目位置来自Inventory行政区配置；未配置时不生成地图点。历史/测试运行源不计作真实接入。
    </p>
    <p v-else>下方为空间沙盘：匿名登记 / DEMO 资产，与 Cloud 运行统计分开展示。</p>
  </section>
</template>
<style scoped>
.cloud-runtime {
  flex-shrink: 0;
  margin: 0 28px 10px;
  padding: 10px 16px;
  border: 1px solid #28505b;
  background: #102730;
  font-size: 11px;
}
.runtime-title {
  display: flex;
  justify-content: space-between;
  color: #7bd8db;
}
.runtime-metrics {
  display: flex;
  gap: 36px;
  margin: 8px 0;
  flex-wrap: wrap;
}
.runtime-metrics span {
  color: #92a4af;
  margin-right: 12px;
}
.runtime-metrics b {
  font-size: 22px;
  margin-right: 6px;
}
p {
  color: #92a4af;
  margin-top: 4px;
  line-height: 1.5;
}
</style>
