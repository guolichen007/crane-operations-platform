<script setup lang="ts">
// KPI strip (≤5 cards). Each card is a button that applies its status filter
// (shared with the Attention list via the store); the active filter is shown as
// pressed. Numbers come from homeKpis()/homeMetrics() over the statistics scope
// (H1-R1-2); the scope label states exactly what the numbers count.
// Data state (H1-R1-1): loading → "—", unavailable → "不可用" (never 0),
// stale → last good numbers marked 过期.
import type { HomeDataState, HomeKpi } from '../../scene/home-metrics';
defineProps<{ kpis: HomeKpi[]; active: string; state: HomeDataState }>();
const emit = defineEmits<{ apply: [filter: string, id: string] }>();
</script>
<template>
  <section
    class="home-kpis"
    :class="'state-' + state"
    aria-label="核心指标（点击即过滤）"
    data-testid="home-kpis"
    :data-state="state"
    :aria-busy="state === 'loading' || state === 'refreshing'"
  >
    <button
      v-for="k in kpis"
      :key="k.id"
      type="button"
      class="home-kpi"
      :class="['tone-' + k.tone, { selected: k.filter !== 'all' && active === k.filter }]"
      :data-kpi="k.id"
      :title="k.hint"
      :aria-pressed="k.filter !== 'all' && active === k.filter"
      @click="emit('apply', k.filter !== 'all' && active === k.filter ? 'all' : k.filter, k.id)"
    >
      <span class="home-kpi-label">{{ k.label }}<i v-if="k.tag" class="home-kpi-tag">{{ k.tag }}</i></span>
      <i v-if="state === 'stale'" class="home-stale-tag">已过期</i>
      <b class="home-kpi-value" :data-value="k.value ?? ''"
        ><template v-if="state === 'unavailable'">不可用</template
        ><template v-else-if="k.value === null">—</template
        ><template v-else>{{ k.value }}<small>{{ k.unit }}</small></template></b
      >
      <em class="home-kpi-detail">{{ state === 'unavailable' ? 'Inventory API 拉取失败' : k.detail }}</em>
    </button>
  </section>
</template>
