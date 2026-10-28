<script setup lang="ts">
import { Map, FolderKanban, Container, TriangleAlert, CircleHelp } from 'lucide-vue-next';
const nav = [
  { path: '/overview', name: '总览', icon: Map },
  { path: '/projects', name: '项目', icon: FolderKanban },
  { path: '/devices', name: '设备', icon: Container },
  { path: '/alarms', name: '告警', icon: TriangleAlert },
];
</script>
<template>
  <aside class="side-rail">
    <span class="rail-index">OPS</span>
    <nav aria-label="业务导航">
      <RouterLink v-for="n in nav" :key="n.path" :to="n.path" active-class="active" :title="n.name"
        ><component :is="n.icon" :size="20" :stroke-width="1.7" /><span>{{
          n.name
        }}</span></RouterLink
      >
    </nav>
    <div class="rail-foot"><CircleHelp :size="18" /><small>A1.1</small></div>
  </aside>
</template>
