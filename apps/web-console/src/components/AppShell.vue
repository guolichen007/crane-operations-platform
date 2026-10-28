<script setup lang="ts">
import { defineAsyncComponent } from 'vue';
import { productionAuth } from '../cloud/session';
import TopCommandBar from './TopCommandBar.vue';
import SideRail from './SideRail.vue';
const cloudMode = import.meta.env.VITE_DATA_MODE === 'api';
const CloudStatus = defineAsyncComponent(() => import('./CloudStatus.vue'));
</script>
<template>
  <CloudStatus v-if="cloudMode && !productionAuth" />
  <div class="app-shell premium-shell" data-testid="app-shell">
    <TopCommandBar />
    <div class="shell-body"><SideRail /><div class="application"><RouterView /></div></div>
  </div>
</template>
