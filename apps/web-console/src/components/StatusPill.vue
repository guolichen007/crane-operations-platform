<script setup lang="ts">
import { computed } from 'vue';
import { statusMapping } from '../design/tokens';
const props = defineProps<{ status?: string; label?: string }>();
const mapped = computed(() => statusMapping[props.status ?? 'UNKNOWN'] ?? statusMapping.UNKNOWN!);
</script>
<template>
  <span :class="['status-pill', 'tone-' + mapped.tone]"><i></i>{{ label ?? mapped.label }}</span>
</template>
