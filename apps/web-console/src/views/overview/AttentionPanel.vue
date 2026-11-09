<script setup lang="ts">
// "需要关注" list. Rows come from the page: API mode = independent operations-summary fields
// (scene/home-summary.ts summaryAttention — overlapping REAL states are listed, never summed);
// demo mode = local fixture metrics. A row with a filter is a button (same store filter as the KPI
// strip); a row without one is informational. Work orders and inspections have no backend yet and
// are shown as "未接入" — no number, not clickable. Same data state as the KPI strip (no fake 0).
import type { HomeDataState } from '../../scene/home-metrics';
export type AttentionRowView = { id: string; label: string; value: number | null; filter?: string; color: string; hint: string };
const props = defineProps<{ rows: AttentionRowView[]; state: HomeDataState; active: string; compact: boolean; demo: boolean; note?: string }>();
const shown = (r: AttentionRowView) => (r.value !== null ? String(r.value) : props.state === 'unavailable' ? '不可用' : '—');
const emit = defineEmits<{ apply: [filter: string] }>();
function toggle(filter: string) {
  emit('apply', props.active === filter ? 'all' : filter);
}
</script>
<template>
  <aside class="home-attention" :class="['state-' + state, { compact }]" aria-label="需要关注" data-testid="home-attention" :data-state="state">
    <h3 v-if="!compact">需要关注<small>{{ demo ? '按严重度 · DEMO' : '各项独立统计，不相加' }}<template v-if="state === 'stale'"> · 已过期</template></small></h3>
    <template v-for="r in rows" :key="r.id">
      <button
        v-if="r.filter"
        type="button"
        class="attention-row"
        :class="{ selected: active === r.filter, zero: r.value === 0 }"
        :data-attention="r.id"
        :aria-pressed="active === r.filter"
        :aria-label="`${r.label} ${shown(r)}${state === 'stale' ? '（已过期）' : ''}`"
        :title="r.hint"
        @click="toggle(r.filter)"
      >
        <i :style="{ background: r.color }"></i><span>{{ r.label }}</span><b>{{ shown(r) }}</b>
      </button>
      <div
        v-else
        class="attention-row info"
        :class="{ zero: r.value === 0 }"
        :data-attention="r.id"
        :aria-label="`${r.label} ${shown(r)}${state === 'stale' ? '（已过期）' : ''}`"
        :title="r.hint"
      >
        <i :style="{ background: r.color }"></i><span>{{ r.label }}</span><b>{{ shown(r) }}</b>
      </div>
    </template>
    <div class="attention-row unavailable" data-attention="work-orders" title="维护工单尚未接入" aria-label="维护工单 未接入">
      <i></i><span>维护工单</span><b>未接入</b>
    </div>
    <div class="attention-row unavailable" data-attention="inspection" title="检验到期尚未接入" aria-label="检验到期 未接入">
      <i></i><span>检验到期</span><b>未接入</b>
    </div>
    <p v-if="!compact" class="attention-note">{{ note ?? '点击条目或指标卡即过滤地图与列表，再次点击取消。' }}</p>
  </aside>
</template>
