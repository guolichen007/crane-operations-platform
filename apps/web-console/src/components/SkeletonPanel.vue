<script setup lang="ts">
withDefaults(defineProps<{ label?: string }>(), { label: '正在加载' });
</script>
<template>
  <div class="skeleton-panel" role="status" aria-busy="true">{{ label }}<i></i><i></i></div>
</template>
