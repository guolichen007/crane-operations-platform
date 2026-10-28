<script setup lang="ts">defineProps<{ title?: string; subtitle?: string }>();</script>
<template><section class="glass-panel"><header v-if="title"><h2>{{ title }}</h2><small>{{ subtitle }}</small><slot name="action" /></header><slot /></section></template>
