<script setup lang="ts">
import { X } from 'lucide-vue-next';
import GlassPanel from './GlassPanel.vue';
defineProps<{ title: string }>();
defineEmits<{ close: [] }>();
</script>
<template>
  <GlassPanel class="right-inspector" :title="title"
    ><template #action
      ><button class="inspector-close" aria-label="关闭详情" @click="$emit('close')">
        <X :size="16" /></button></template
    ><slot
  /></GlassPanel>
</template>
