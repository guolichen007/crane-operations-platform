<script setup lang="ts">
import { computed, ref } from 'vue';
import { useRoute } from 'vue-router';
import { useQuery } from '@tanstack/vue-query';
import { Search, MapPin } from 'lucide-vue-next';
import { listAllLocations } from '../cloud/asset';
import { webSession } from '../cloud/session';
import type { AssetLocation } from '../../../../contracts/typescript/types.gen';

const route = useRoute();
const q = ref('');
const kind = ref('');
const status = ref('');
// The project scope follows the top command bar (?project=), falling back to the
// first authorized project — never silently only the first one.
const projectId = computed(() => {
  const scoped = typeof route.query.project === 'string' ? route.query.project : '';
  if (scoped) return scoped;
  return webSession.value?.projects?.[0]?.projectId ?? '';
});

const locationsQuery = useQuery({
  queryKey: ['locations', projectId],
  queryFn: () => listAllLocations(projectId.value, { sort: 'name' }),
  enabled: computed(() => !!projectId.value),
});

const kindOptions = ['SITE', 'PLANT', 'WORKSHOP', 'BAY', 'RUNWAY', 'OTHER'];
const kindText: Record<string, string> = {
  SITE: '场地', PLANT: '厂区', WORKSHOP: '车间', BAY: '工位', RUNWAY: '轨道', OTHER: '其他',
};

type LocationNode = AssetLocation & { children: LocationNode[] };
// Full hierarchy (SITE → PLANT → WORKSHOP → BAY → RUNWAY): build the nested tree
// from the flat parentId list, so every level is expressed instead of a flat
// two-level "lazy tree".
const fullTree = computed<LocationNode[]>(() => {
  const items = (locationsQuery.data.value ?? []) as LocationNode[];
  const byId = new Map<string, LocationNode>();
  for (const l of items) byId.set(l.id, { ...l, children: [] });
  const roots: LocationNode[] = [];
  for (const node of byId.values()) {
    if (node.parentId && byId.has(node.parentId)) byId.get(node.parentId)!.children.push(node);
    else roots.push(node);
  }
  return roots;
});
// Client-side filter with ancestor preservation: a node is kept when it matches
// (name/code + kind + status) or when any descendant matches, so a matching BAY
// never masquerades as a top-level SITE after filtering.
const tree = computed<LocationNode[]>(() => {
  const ql = q.value.trim().toLowerCase();
  const match = (n: AssetLocation) =>
    (kind.value === '' || n.kind === kind.value) &&
    (status.value === '' || n.status === status.value) &&
    (ql === '' || n.name.toLowerCase().includes(ql) || (n.code ?? '').toLowerCase().includes(ql));
  const prune = (nodes: LocationNode[]): LocationNode[] => {
    const out: LocationNode[] = [];
    for (const n of nodes) {
      const children = prune(n.children);
      if (match(n) || children.length > 0) out.push({ ...n, children });
    }
    return out;
  };
  return prune(fullTree.value);
});
const flattened = computed(() => {
  const out: { node: LocationNode; depth: number }[] = [];
  const walk = (nodes: LocationNode[], depth: number) => {
    for (const n of nodes) {
      out.push({ node: n, depth });
      walk(n.children, depth + 1);
    }
  };
  walk(tree.value, 0);
  return out;
});
</script>
<template>
  <section class="workspace-page location-center" data-testid="location-center">
    <header class="workspace-page-head">
      <div>
        <span class="eyebrow">LOCATION CENTER / 位置中心</span>
        <h1>位置层级</h1>
        <p>SITE → PLANT → WORKSHOP → BAY → RUNWAY · 完整层级</p>
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
    <p v-else-if="locationsQuery.isError.value" class="workspace-error" role="alert">
      位置加载失败：{{ (locationsQuery.error.value as Error).message }}
    </p>
    <p v-else-if="!projectId" class="workspace-empty">请先在顶部选择项目范围。</p>
    <p v-else-if="!flattened.length" class="workspace-empty">暂无位置数据。</p>
    <div v-else class="data-table-wrap">
      <table class="data-table">
        <thead><tr><th>名称</th><th>编号</th><th>类型</th><th>状态</th><th>地址</th></tr></thead>
        <tbody>
          <tr v-for="row in flattened" :key="row.node.id" class="data-row" :class="row.depth ? 'tree-child' : 'tree-root'">
            <td :style="{ paddingLeft: 12 + row.depth * 16 + 'px' }">{{ row.depth ? '└ ' : '' }}{{ row.node.name }}</td>
            <td>{{ row.node.code ?? '—' }}</td>
            <td>{{ kindText[row.node.kind] ?? row.node.kind }}</td>
            <td>{{ row.node.status }}</td>
            <td>{{ row.node.address || '—' }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
