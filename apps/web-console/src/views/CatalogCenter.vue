<script setup lang="ts">
import { ref } from 'vue';
import { useQuery } from '@tanstack/vue-query';
import { BookOpen, RefreshCw } from 'lucide-vue-next';
import { listManufacturers, listCatalog } from '../cloud/asset';
import { ApiError } from '../cloud/client';

const tab = ref<'manufacturer' | 'catalog'>('manufacturer');

const manufacturersQuery = useQuery({
  queryKey: ['manufacturers'],
  queryFn: () => listManufacturers(),
});
const catalogQuery = useQuery({
  queryKey: ['catalog'],
  queryFn: () => listCatalog(),
  enabled: () => tab.value === 'catalog',
});
// Q4-QA-R1 §5: an error is never shown as "no data" (503 ≠ 暂无目录); each state is explicit.
function loadError(e: unknown): string {
  if (e instanceof ApiError && e.status === 403) return '没有查看权限。';
  if (e instanceof ApiError && e.status === 503) return '服务暂不可用，目录未能加载。';
  if (e instanceof ApiError) return `加载失败（${e.status}）：${e.message}`;
  return '网络连接失败或请求超时，目录未能加载。';
}
</script>
<template>
  <section class="workspace-page catalog-center" data-testid="catalog-center">
    <header class="workspace-page-head">
      <div>
        <span class="eyebrow">EQUIPMENT CATALOG / 设备目录</span>
        <h1>设备目录</h1>
        <p>设备类型 · 制造商 · 型号目录 · Global 只读</p>
      </div>
      <BookOpen :size="20" />
    </header>

    <div class="tabs" role="tablist">
      <button :class="{ active: tab === 'manufacturer' }" @click="tab = 'manufacturer'">制造商</button>
      <button :class="{ active: tab === 'catalog' }" @click="tab = 'catalog'">型号目录</button>
    </div>

    <template v-if="tab === 'manufacturer'">
      <p v-if="manufacturersQuery.isLoading.value" class="skeleton table-skeleton" aria-busy="true" data-testid="manufacturer-loading"></p>
      <p v-else-if="manufacturersQuery.isError.value" class="workspace-error" role="alert" data-testid="manufacturer-error">
        制造商{{ loadError(manufacturersQuery.error.value) }}
        <button type="button" class="ghost-action" @click="manufacturersQuery.refetch()"><RefreshCw :size="13" />重试</button>
      </p>
      <p v-else-if="!manufacturersQuery.data.value?.length" class="workspace-empty" data-testid="manufacturer-empty">暂无制造商。</p>
      <div v-else class="data-table-wrap">
        <table class="data-table" data-testid="manufacturer-table">
          <thead><tr><th>名称</th><th>简称</th><th>国家/地区</th><th>状态</th><th>范围</th></tr></thead>
          <tbody>
            <tr v-for="m in manufacturersQuery.data.value" :key="m.id" class="data-row">
              <td>{{ m.name }}</td>
              <td>{{ m.shortName || '—' }}</td>
              <td>{{ m.countryRegion ?? '—' }}</td>
              <td>{{ m.status }}</td>
              <td>
                <span v-if="m.projectId === null" class="status-pill" data-status="DRAFT">GLOBAL · 只读</span>
                <span v-else class="status-pill" data-status="ACTIVE">项目级</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <template v-else>
      <p v-if="catalogQuery.isLoading.value" class="skeleton table-skeleton" aria-busy="true" data-testid="catalog-loading"></p>
      <p v-else-if="catalogQuery.isError.value" class="workspace-error" role="alert" data-testid="catalog-error">
        型号目录{{ loadError(catalogQuery.error.value) }}
        <button type="button" class="ghost-action" @click="catalogQuery.refetch()"><RefreshCw :size="13" />重试</button>
      </p>
      <p v-else-if="!catalogQuery.data.value?.length" class="workspace-empty" data-testid="catalog-empty">暂无型号目录。</p>
      <div v-else class="data-table-wrap">
        <table class="data-table" data-testid="catalog-table">
          <thead><tr><th>名称</th><th>类型</th><th>品牌</th><th>型号</th><th>规格版本</th><th>范围</th></tr></thead>
          <tbody>
            <tr v-for="c in catalogQuery.data.value" :key="c.id" class="data-row">
              <td>{{ c.name }}</td>
              <td>{{ c.typeCode }}</td>
              <td>{{ c.brand }}</td>
              <td>{{ c.model }}</td>
              <td>{{ c.specSchemaVersion }}</td>
              <td>
                <span v-if="c.projectId === null" class="status-pill" data-status="DRAFT">GLOBAL · 只读</span>
                <span v-else class="status-pill" data-status="ACTIVE">项目级</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </section>
</template>
