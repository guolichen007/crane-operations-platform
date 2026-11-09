<script setup lang="ts">
// Region navigation + data source filter. Rendered in normal document flow
// (never absolutely overlaid), so its buttons cannot be covered by other
// sections (H0-D4). Drilling goes through the store; the active spatial view
// converges to it.
// H2-1D-3: in API mode each child region shows its server count (summary.regions[], direct children of
// the displayed scope), and the partition line states registered = Σ children + scope level + unlocated
// — the children alone need not add up to the total.
// H2-1D-4: the API-mode "data source" filter was removed. Every API project is a registered Inventory
// project, so "登记项目" filtered nothing and read like a REAL-connection filter; runtime source
// (REAL / SIM / TEST) is a property of runtime identities and is reported by the KPI cards.
import { computed, ref } from 'vue';
import type { OperationsSummary } from '../../../../../contracts/typescript/types.gen';
import type { HomeDataState } from '../../scene/home-metrics';
import { regionPartition } from '../../scene/home-summary';
import { storeToRefs } from 'pinia';
import { ChevronRight, SlidersHorizontal } from 'lucide-vue-next';
import { useOperations } from '../../stores/operations';
import { sceneProvider } from '../../scene/provider';

const props = defineProps<{ demo: boolean; summary?: OperationsSummary | null; state?: HomeDataState }>();
const store = useOperations();
const { source, regionStack, regionChildren } = storeToRefs(store);
const regionSearch = ref('');
// Server counts per direct child of the displayed scope (API). Only trusted when the summary is for
// the displayed scope; otherwise no number is shown.
const scopeLeaf = computed(() => regionStack.value[regionStack.value.length - 1]?.adcode ?? 100000);
const counts = computed(() => {
  const s = props.summary;
  if (props.demo || !s || (s.scope.region ?? 100000) !== scopeLeaf.value) return null;
  return new Map(s.regions.map((r) => [r.adcode, r.registered]));
});
const partition = computed(() => (counts.value && props.summary ? regionPartition(props.summary) : null));
const navigation = computed(() => {
  const real = sceneProvider.realProjects.value;
  const c = counts.value;
  const weight = (code: number) => (c ? c.get(code) ?? 0 : Number(real.some((p) => p.adminPath.includes(code))));
  return regionChildren.value
    .filter((r) => r.name.includes(regionSearch.value))
    .slice()
    .sort((a, b) => weight(b.adcode) - weight(a.adcode));
});
function enterRegion(code: number) {
  regionSearch.value = '';
  store.navigateRegion([...regionStack.value.slice(1).map((l) => l.adcode), code]);
}
const shortName = (n: string) =>
  n
    .replace('壮族自治区', '')
    .replace('维吾尔自治区', '')
    .replace('回族自治区', '')
    .replace('藏族羌族自治州', '州')
    .replace('哈尼族彝族自治州', '州');
</script>
<template>
  <section class="home-region-nav region-navigation" aria-label="行政区导航" data-testid="region-nav">
    <div class="panel-title">
      <h3>{{ regionStack.length === 1 ? '行政区导航' : regionStack[regionStack.length - 1]?.name }}</h3>
      <span>点击钻取</span>
    </div>
    <input v-if="regionChildren.length > 8" v-model="regionSearch" class="region-search" placeholder="搜索行政区域" aria-label="搜索行政区域" />
    <div class="region-chips">
      <button v-for="r in navigation" :key="r.adcode" type="button" :data-region-code="r.adcode" @click="enterRegion(r.adcode)">
        {{ shortName(r.name) }}<b v-if="counts" class="region-count" :data-region-count="counts.get(r.adcode) ?? 0">{{ counts.get(r.adcode) ?? 0 }}</b><ChevronRight :size="11" />
      </button>
      <span v-if="!regionChildren.length" class="muted small">已到达最下级区域，点击项目或行车查看详情。</span>
    </div>
    <p v-if="partition" class="region-partition" data-testid="region-partition"
      :data-children="partition.children" :data-at-scope="partition.atScopeLevel" :data-unlocated="partition.unlocated" :data-registered="partition.registered"
      title="登记设备 = 各下级区域之和 + 挂在当前区域本级（无法下钻）的设备 + 无空间位置的设备（仅全国范围）">
      登记 {{ partition.registered }} = 下级 {{ partition.children }} + 本级 {{ partition.atScopeLevel }}<template v-if="regionStack.length === 1"> + 无位置 {{ partition.unlocated }}</template>
    </p>
    <p v-else-if="!demo" class="region-partition muted" data-testid="region-partition">{{ state === 'unavailable' ? '区域统计不可用' : '区域统计加载中…' }}</p>
  </section>
  <section v-if="demo" class="home-source" aria-label="数据来源筛选">
    <div class="panel-title"><h3>数据来源</h3><SlidersHorizontal :size="13" /></div>
    <div class="segmented source-filter">
      <button
        v-for="s in [
          { id: 'all', name: '全部展示' },
          { id: 'real', name: '匿名登记项目' },
          { id: 'demo', name: '演示设备' },
        ]"
        :key="s.id"
        type="button"
        :class="{ selected: source === s.id }"
        @click="source = s.id as typeof source"
      >
        {{ s.name }}
      </button>
    </div>
  </section>
</template>
