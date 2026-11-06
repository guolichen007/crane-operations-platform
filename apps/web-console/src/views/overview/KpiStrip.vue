<script setup lang="ts">
// KPI strip (≤5 cards). Each card is a button that applies its status filter
// (shared with the Attention list via the store); the active filter is shown as
// pressed. Numbers come from homeKpis()/homeMetrics() only.
import type { HomeKpi } from '../../scene/home-metrics';
defineProps<{ kpis: HomeKpi[]; active: string; loading: boolean }>();
const emit = defineEmits<{ apply: [filter: string] }>();
</script>
<template>
  <section class="home-kpis" aria-label="核心指标（点击即过滤）" data-testid="home-kpis">
    <button
      v-for="k in kpis"
      :key="k.id"
      type="button"
      class="home-kpi"
      :class="['tone-' + k.tone, { selected: k.filter !== 'all' && active === k.filter }]"
      :data-kpi="k.id"
      :aria-pressed="k.filter !== 'all' && active === k.filter"
      @click="emit('apply', k.filter !== 'all' && active === k.filter ? 'all' : k.filter)"
    >
      <span class="home-kpi-label">{{ k.label }}<i v-if="k.tag" class="home-kpi-tag">{{ k.tag }}</i></span>
      <b class="home-kpi-value">{{ loading || k.value === null ? '—' : k.value }}<small>{{ k.unit }}</small></b>
      <em class="home-kpi-detail">{{ k.detail }}</em>
    </button>
  </section>
</template>
