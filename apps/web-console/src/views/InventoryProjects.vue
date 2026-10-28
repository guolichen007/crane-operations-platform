<script setup lang="ts">
import { computed, ref } from 'vue';
import { useRoute } from 'vue-router';
import { ArrowUpRight, MapPin, Box, Search } from 'lucide-vue-next';
import { listProjects } from '../cloud/api';
import { useCloudQuery } from '../cloud/useCloudQuery';
import { lifecycleName, projectConnection } from '../cloud/inventory';
import StatusPill from '../components/StatusPill.vue';
import SkeletonPanel from '../components/SkeletonPanel.vue';
const { data, error, loading } = useCloudQuery(listProjects);
const route = useRoute(),
  search = ref(''),
  lifecycle = ref('');
const projects = computed(
  () =>
    data.value?.filter(
      (p) =>
        (!route.query.project || p.id === route.query.project) &&
        (!lifecycle.value || p.lifecycle === lifecycle.value) &&
        (p.id + p.name).toLowerCase().includes(search.value.toLowerCase())
    ) ?? []
);
</script>
<template>
  <main class="premium-workspace">
    <div class="premium-page-heading">
      <div>
        <p class="premium-eyebrow">PROJECT PORTFOLIO / 项目中心</p>
        <h1>全国项目 <span>从资产规划到运行接入</span></h1>
      </div>
      <span class="data-origin">{{ data?.length ?? '—' }} PROJECTS / CLOUD API</span>
    </div>
    <div class="premium-filterbar">
      <label class="premium-search"
        ><Search :size="14" /><input
          v-model="search"
          aria-label="搜索项目"
          placeholder="搜索项目名称 / 编号" /></label
      ><select v-model="lifecycle" aria-label="项目生命周期">
        <option value="">全部生命周期</option>
        <option v-for="(name, key) in lifecycleName" :key="key" :value="key">
          {{ name }}
        </option></select
      ><span>资产位置仅表示行政区域</span>
    </div>
    <p v-if="error" role="status">{{ error }}；{{ data ? '保留上次结果' : '尚未读取项目' }}</p>
    <SkeletonPanel v-else-if="loading" label="正在读取项目…" />
    <p v-else-if="!data?.length" class="no-data">
      尚未导入 Inventory；请由管理员通过私有文件导入。
    </p>
    <div class="premium-project-grid">
      <section
        v-for="p in projects"
        :key="p.id"
        class="premium-project-card"
        data-testid="inventory-project"
      >
        <div class="project-thumbnail">
          <span>{{ p.id }} / ASSET SITE</span>
          <div class="site-blueprint"><i></i><i></i><i></i><b></b></div>
          <small>区域示意 · 非现场影像</small>
        </div>
        <div class="project-card-body">
          <div class="project-card-top">
            <small>{{ p.id }}</small
            ><StatusPill :label="lifecycleName[p.lifecycle]" />
          </div>
          <h2>{{ p.name }}</h2>
          <p class="project-card-location">
            <MapPin :size="12" />{{ p.locationLabel || '行政区未配置' }} · 行政区级定位
          </p>
          <p class="project-card-source">{{ projectConnection(p) }}</p>
          <div class="project-card-stats">
            <span
              ><Box :size="13" /> 行车资产 <b>{{ p.assetDeviceCount }}</b></span
            ><span
              >模拟 <b>{{ p.simulatedCount }}</b></span
            ><span
              >真实 <b>{{ p.realConnectedCount }}</b></span
            >
          </div>
          <p class="project-card-plan">{{ p.scheduleText || '实施计划待补充' }}</p>
          <div class="project-card-links">
            <RouterLink :to="'/overview?project=' + encodeURIComponent(p.id)"
              >沙盘定位 <ArrowUpRight :size="13" /></RouterLink
            ><RouterLink :to="'/devices?project=' + encodeURIComponent(p.id)"
              >查看该项目资产 <ArrowUpRight :size="13"
            /></RouterLink>
          </div>
        </div>
      </section>
    </div>
    <p v-if="data?.length && !projects.length" class="no-data">无匹配项目</p>
  </main>
</template>
