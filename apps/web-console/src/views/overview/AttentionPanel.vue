<script setup lang="ts">
// Attention list, ordered by severity. The first four rows are filters (same
// store filter as the KPI strip); work orders and inspection due dates have no
// backend yet and are shown as "未接入" — no number, not clickable.
import type { HomeMetrics } from '../../scene/home-metrics';
const props = defineProps<{ metrics: HomeMetrics | null; active: string; compact: boolean; demo: boolean }>();
const emit = defineEmits<{ apply: [filter: string] }>();
type Row = { id: string; label: string; filter: string; value: (m: HomeMetrics) => number; color: string };
const rows: Row[] = [
  { id: 'alarm', label: '告警设备', filter: 'ALARM', value: (m) => m.alarmDevices, color: 'var(--critical)' },
  { id: 'offline', label: '离线 / 未知', filter: 'OFFLINE', value: (m) => m.offline, color: '#7b8ea3' },
  { id: 'not-commissioned', label: '尚未投运', filter: 'NOT_COMMISSIONED', value: (m) => m.notCommissioned, color: 'var(--warning)' },
  { id: 'unlocated', label: '无空间位置', filter: 'UNLOCATED', value: (m) => m.unlocated, color: '#5c7791' },
];
function toggle(filter: string) {
  emit('apply', props.active === filter ? 'all' : filter);
}
</script>
<template>
  <aside class="home-attention" :class="{ compact }" aria-label="需要关注" data-testid="home-attention">
    <h3 v-if="!compact">需要关注<small>按严重度{{ demo ? ' · DEMO' : '' }}</small></h3>
    <button
      v-for="r in rows"
      :key="r.id"
      type="button"
      class="attention-row"
      :class="{ selected: active === r.filter, zero: metrics && r.value(metrics) === 0 }"
      :data-attention="r.id"
      :aria-pressed="active === r.filter"
      :aria-label="`${r.label} ${metrics ? r.value(metrics) : '加载中'}`"
      :title="r.label"
      @click="toggle(r.filter)"
    >
      <i :style="{ background: r.color }"></i><span>{{ r.label }}</span><b>{{ metrics ? r.value(metrics) : '—' }}</b>
    </button>
    <div class="attention-row unavailable" data-attention="work-orders" title="维护工单尚未接入" aria-label="维护工单 未接入">
      <i></i><span>维护工单</span><b>未接入</b>
    </div>
    <div class="attention-row unavailable" data-attention="inspection" title="检验到期尚未接入" aria-label="检验到期 未接入">
      <i></i><span>检验到期</span><b>未接入</b>
    </div>
    <p v-if="!compact" class="attention-note">点击条目或指标卡即过滤地图与列表，再次点击取消。</p>
  </aside>
</template>
