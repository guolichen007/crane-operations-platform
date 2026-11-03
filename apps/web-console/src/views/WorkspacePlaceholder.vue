<script setup lang="ts">
import { Wrench } from 'lucide-vue-next';
defineProps<{ title: string; description?: string }>();
</script>
<template>
  <section class="workspace-page" data-testid="workspace-placeholder">
    <header class="workspace-page-head">
      <div>
        <span class="eyebrow">INDUSTRIAL WORKSPACE</span>
        <h1>{{ title }}</h1>
        <p>{{ description ?? '该工作区正在接入 B2/B3 业务能力。' }}</p>
      </div>
      <Wrench :size="20" />
    </header>
    <div class="workspace-page-body">
      <slot>工作区骨架已就绪，业务视图即将填充。</slot>
    </div>
  </section>
</template>
