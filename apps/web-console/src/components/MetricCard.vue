<script setup lang="ts">
import MiniTrend from './MiniTrend.vue';
defineProps<{ label: string; value?: string | number; note?: string; trend?: number[] }>();
</script>
<template><div class="metric-card"><span>{{ label }}</span><strong>{{ value ?? '—' }}</strong><small>{{ note }}</small><MiniTrend v-if="trend?.length" :values="trend" /></div></template>
