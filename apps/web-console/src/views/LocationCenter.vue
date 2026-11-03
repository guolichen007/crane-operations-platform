<script setup lang="ts">
import { computed, ref } from 'vue';
import { useQuery } from '@tanstack/vue-query';
import { Search, MapPin } from 'lucide-vue-next';
import { listLocations } from '../cloud/asset';
import { webSession } from '../cloud/session';

const q = ref('');
const kind = ref('');
const status = ref('');
const projectId = computed(() => webSession.value?.projects?.[0]?.projectId ?? '');

const locationsQuery = useQuery({
  queryKey: ['locations', projectId, q, kind, status],
  queryFn: () => listLocations(projectId.value, { q: q.value, sort: 'name' }),
  enabled: computed(() => !!projectId.value),
});

const kindOptions = ['SITE', 'PLANT', 'WORKSHOP', 'BAY', 'RUNWAY', 'OTHER'];
const kindText: Record<string, string> = {
  SITE: '场地', PLANT: '厂区', WORKSHOP: '车间', BAY: '工位', RUNWAY: '轨道', OTHER: '其他',
};

// Lazy tree: group children under their parent for a two-level hierarchy.
const tree = computed(() => {
  const items = locationsQuery.data.value ?? [];
  const roots = items.filter((l) => !l.parentId);
  const children = new Map<string, typeof items>();
  for (const l of items) if (l.parentId) children.set(l.parentId, [...(children.get(l.parentId) ?? []), l]);
  return roots.map((r) => ({ root: r, children: children.get(r.id) ?? [] }));
});
</script>
<template>
  <section class="workspace-page location-center" data-testid="location-center">
    <header class="workspace-page-head">
      <div>
        <span class="eyebrow">LOCATION CENTER / 位置中心</span>
        <h1>位置层级</h1>
        <p>SITE → PLANT → WORKSHOP → BAY → RUNWAY · 按需展开的懒加载树</p>
      </div>
      <MapPin :size="20" />
    </header>

    <div class="asset-filters">
      <div class="search-field">
        <Search :size="15" />
        <input v-model="q" placeholder="搜索名称 / 编号" />
      </div>
      <select v-model="kind" aria-label="类型筛选">
        <option value="">全部类型</option>
        <option v-for="k in kindOptions" :key="k" :value="k">{{ kindText[k] ?? k }}</option>
      </select>
      <select v-model="status" aria-label="状态筛选">
        <option value="">全部状态</option>
        <option value="ACTIVE">在用</option>
        <option value="DISABLED">停用</option>
      </select>
    </div>

    <p v-if="locationsQuery.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></p>
    <p v-else-if="!projectId" class="workspace-empty">请先在顶部选择项目范围。</p>
    <p v-else-if="!tree.length" class="workspace-empty">暂无位置数据。</p>
    <div v-else class="data-table-wrap">
      <table class="data-table">
        <thead><tr><th>名称</th><th>编号</th><th>类型</th><th>状态</th><th>地址</th></tr></thead>
        <tbody>
          <template v-for="node in tree" :key="node.root.id">
            <tr class="data-row tree-root">
              <td>{{ node.root.name }}</td>
              <td>{{ node.root.code ?? '—' }}</td>
              <td>{{ kindText[node.root.kind] ?? node.root.kind }}</td>
              <td>{{ node.root.status }}</td>
              <td>{{ node.root.address || '—' }}</td>
            </tr>
            <tr v-for="child in node.children" :key="child.id" class="data-row tree-child">
              <td>└ {{ child.name }}</td>
              <td>{{ child.code ?? '—' }}</td>
              <td>{{ kindText[child.kind] ?? child.kind }}</td>
              <td>{{ child.status }}</td>
              <td>{{ child.address || '—' }}</td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>
  </section>
</template>
