<script setup lang="ts">
import { listProjects } from '../cloud/api';
import { useCloudQuery } from '../cloud/useCloudQuery';
import { lifecycleName, projectConnection } from '../cloud/inventory';
const { data, error, loading } = useCloudQuery(listProjects);
</script>
<template>
  <main class="workspace-page cloud-workspace">
    <div class="page-heading">
      <div>
        <div class="eyebrow">INVENTORY / CLOUD API</div>
        <h1>项目资产台账</h1>
        <p>项目生命周期与运行来源分开管理 · 行政区级定位</p>
      </div>
    </div>
    <p v-if="error" role="status">{{ error }}；{{ data ? '保留上次结果' : '尚未读取项目' }}</p>
    <p v-else-if="loading">正在读取项目…</p>
    <p v-else-if="!data?.length">尚未导入 Inventory；请由管理员通过私有文件导入。</p>
    <div class="inventory-cards">
      <section v-for="p in data" :key="p.id" class="inventory-card" data-testid="inventory-project">
        <small>{{ p.id }} · {{ lifecycleName[p.lifecycle] }}</small>
        <h2>{{ p.name }}</h2>
        <p>{{ p.locationLabel || '行政区未配置' }} · 行政区级定位</p>
        <p>{{ projectConnection(p) }}</p>
        <p>
          行车资产 {{ p.assetDeviceCount }} · 模拟运行源 {{ p.simulatedCount }} · 真实在线
          {{ p.realConnectedCount }}
        </p>
        <p>{{ p.scheduleText || '实施计划待补充' }}</p>
        <router-link :to="`/devices?project=${encodeURIComponent(p.id)}`"
          >查看该项目资产 →</router-link
        >
      </section>
    </div>
  </main>
</template>
