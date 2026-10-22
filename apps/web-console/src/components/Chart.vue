<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue';
import * as echarts from 'echarts';
import type { EChartsOption } from 'echarts';
const props = defineProps<{ option: EChartsOption }>();
const host = ref<HTMLDivElement>();
let chart: echarts.ECharts | undefined, observer: ResizeObserver | undefined;
onMounted(() => {
  chart = echarts.init(host.value!, undefined, { renderer: 'canvas' });
  chart.setOption(props.option);
  observer = new ResizeObserver(() => chart?.resize());
  observer.observe(host.value!);
});
watch(
  () => props.option,
  (o) => chart?.setOption(o)
);
onUnmounted(() => {
  observer?.disconnect();
  chart?.dispose();
});
</script>
<template><div ref="host" class="chart" role="img" aria-label="演示运行数据图表"></div></template>
