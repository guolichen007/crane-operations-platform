<script setup lang="ts">
import { computed } from 'vue';
import { useRoute } from 'vue-router';
import { listAssets, listProjects } from '../cloud/api';
import { useCloudQuery } from '../cloud/useCloudQuery';
import { assetSource, lifecycleName } from '../cloud/inventory';
const route = useRoute();
const { data, error, loading } = useCloudQuery(async () => {
  const [projects, assets] = await Promise.all([listProjects(), listAssets()]);
  return { projects, assets };
});
const assets = computed(
  () =>
    data.value?.assets.filter((a) => !route.query.project || a.projectId === route.query.project) ??
    []
);
const names = computed(() => new Map(data.value?.projects.map((p) => [p.id, p.name])));
</script>
<template>
  <main class="workspace-page cloud-workspace">
    <div class="page-heading">
      <div>
        <div class="eyebrow">ASSET DEVICE / INVENTORY API</div>
        <h1>行车资产台账</h1>
        <p>业务资产与 Cloud/MQTT 运行身份独立，规划模拟不代表现场在线。</p>
      </div>
      <router-link to="/runtime">查看 Runtime Shadow →</router-link>
    </div>
    <p v-if="error" role="status">{{ error }}</p>
    <p v-else-if="loading">正在读取资产…</p>
    <p v-else-if="!assets.length">设备档案待补充 · 尚未接入Cloud；不代表离线故障。</p>
    <div class="inventory-cards">
      <section
        v-for="a in assets"
        :key="a.assetDeviceId"
        class="inventory-card"
        data-testid="inventory-asset"
      >
        <small>{{ a.assetDeviceId }} · {{ lifecycleName[a.lifecycle] }}</small>
        <h2>{{ names.get(a.projectId) }} · {{ a.displayName }}</h2>
        <p>型号：{{ a.craneModel ?? '待补充' }}</p>
        <p>
          额定载重：{{ a.ratedCapacityT ?? '待补充' }}
          <span v-if="a.ratedCapacityT !== null">t</span> · 跨度：{{ a.spanM ?? '待补充' }}
          <span v-if="a.spanM !== null">m</span>
        </p>
        <p>工作级别 {{ a.workClass ?? '待补充' }} · 抓斗 {{ a.grabType ?? '待补充' }}</p>
        <strong>{{ assetSource(a) }}</strong>
        <p v-if="a.runtimeDeviceId">Runtime：{{ a.runtimeDeviceId }}</p>
        <router-link
          v-if="a.runtimeDeviceId"
          :to="`/devices/${encodeURIComponent(a.runtimeDeviceId)}`"
          >最近状态 / LIVE →</router-link
        >
      </section>
    </div>
  </main>
</template>
