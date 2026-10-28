<script setup lang="ts">
import { computed } from 'vue';
const props = defineProps<{ values?: number[] }>();
const points = computed(() => {
  const values = (props.values ?? []).filter(Number.isFinite);
  const min = Math.min(...values),
    span = Math.max(...values) - min || 1;
  return values
    .map((v, i) => `${(i / Math.max(1, values.length - 1)) * 100},${28 - ((v - min) / span) * 24}`)
    .join(' ');
});
</script>
<template>
  <svg
    v-if="values && values.length > 1"
    class="mini-trend"
    viewBox="0 0 100 32"
    role="img"
    aria-label="历史趋势"
  >
    <polyline :points="points" fill="none" stroke="currentColor" stroke-width="1.5" /></svg
  ><span v-else class="mini-trend-empty">暂无趋势</span>
</template>
