<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue';
import * as echarts from 'echarts';
import type { EChartsOption } from 'echarts';
import { udaoTheme } from '../charts/theme';
echarts.registerTheme('udao-dark', udaoTheme);
const props = defineProps<{ option: EChartsOption; state?: 'ready' | 'empty' | 'loading' | 'error'; label?: string }>();
const host = ref<HTMLDivElement>();
let chart: echarts.ECharts | undefined, observer: ResizeObserver | undefined;
onMounted(() => {
  chart = echarts.init(host.value!, 'udao-dark', { renderer: 'canvas' });
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
<template><div class="chart-container"><div v-show="!state || state === 'ready'" ref="host" class="chart" role="img" :aria-label="label ?? '演示运行数据图表'"></div><div v-if="state && state !== 'ready'" class="chart-state" role="status">{{ state === 'empty' ? '暂无数据' : state === 'loading' ? '正在加载图表' : '图表暂不可用' }}</div></div></template>
