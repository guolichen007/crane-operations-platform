<script setup lang="ts">
import CloudAlarms from '../components/CloudAlarms.vue';
</script>
<template>
  <main class="premium-workspace premium-alarm-center">
    <div class="premium-page-heading">
      <div>
        <h1>告警记录 <span>关注异常 · 追溯上下文</span></h1>
      </div>
      <span class="data-origin">云端记录 · 最近 100 条</span>
    </div>
    <CloudAlarms />
  </main>
</template>
