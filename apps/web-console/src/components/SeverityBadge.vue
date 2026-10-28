<script setup lang="ts">
import { computed } from 'vue';
import { severityMapping } from '../design/tokens';
const props = defineProps<{ severity: string }>();
const mapped = computed(() => severityMapping[props.severity.toUpperCase()] ?? { label: props.severity || '未分级', tone: 'muted' });
</script>
<template><span :class="['severity-badge', 'tone-' + mapped.tone]">{{ mapped.label }}</span></template>
