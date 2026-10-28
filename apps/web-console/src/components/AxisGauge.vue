<script setup lang="ts">
import { computed } from 'vue';
const props = defineProps<{ axis: string; label: string; value?: number; min?: number; max?: number; unit?: string }>();
const ratio = computed(() => Math.max(0, Math.min(100, ((props.value ?? 0) - (props.min ?? 0)) / ((props.max ?? 100) - (props.min ?? 0) || 1) * 100)));
</script>
<template><section class="axis-gauge"><header><b>{{ axis }}</b><span>{{ label }}</span></header><strong>{{ value === undefined ? '—' : value.toFixed(1) }} <small>{{ unit ?? 'm' }}</small></strong><div class="axis-track"><i v-if="value !== undefined" :style="{ width: ratio + '%' }"></i></div><small>{{ min ?? 0 }} – {{ max ?? 100 }} {{ unit ?? 'm' }} · 可视范围</small></section></template>
