<script setup lang="ts">
// Region navigation + data source filter. Rendered in normal document flow
// (never absolutely overlaid), so its buttons cannot be covered by other
// sections (H0-D4). Drilling goes through the store; the active spatial view
// converges to it.
import { computed, ref } from 'vue';
import { storeToRefs } from 'pinia';
import { ChevronRight, SlidersHorizontal } from 'lucide-vue-next';
import { useOperations } from '../../stores/operations';
import { sceneProvider } from '../../scene/provider';

defineProps<{ demo: boolean }>();
const store = useOperations();
const { source, regionStack, regionChildren } = storeToRefs(store);
const regionSearch = ref('');
const navigation = computed(() => {
  const real = sceneProvider.realProjects.value;
  const has = (code: number) => real.some((p) => p.adminPath.includes(code));
  return regionChildren.value
    .filter((c) => c.name.includes(regionSearch.value))
    .slice()
    .sort((a, b) => Number(has(b.adcode)) - Number(has(a.adcode)));
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
        {{ shortName(r.name) }}<ChevronRight :size="11" />
      </button>
      <span v-if="!regionChildren.length" class="muted small">已到达最下级区域，点击项目或行车查看详情。</span>
    </div>
  </section>
  <section class="home-source" aria-label="数据来源筛选">
    <div class="panel-title"><h3>数据来源</h3><SlidersHorizontal :size="13" /></div>
    <div class="segmented source-filter">
      <button
        v-for="s in [
          { id: 'all', name: '全部展示' },
          { id: 'real', name: demo ? '匿名登记项目' : '登记项目' },
          { id: 'demo', name: '演示设备' },
        ].filter((x) => demo || x.id !== 'demo')"
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
