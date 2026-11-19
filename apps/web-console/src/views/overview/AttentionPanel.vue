<script setup lang="ts">
// "需要关注" list. Rows come from the page: API mode = independent operations-summary fields
// (scene/home-summary.ts summaryAttention — overlapping REAL states are listed, never summed);
// demo mode = local fixture metrics. A row with a filter is a button (same store filter as the KPI
// strip); a row without one is informational. R9: 维护工单 / 检验到期 come from the maintenance & due
// center (REAL only; links to /maintenance). Without that data (demo mode or the API failed) they
// stay "未接入" / "不可用" — never a fake 0.
import type { HomeDataState } from '../../scene/home-metrics';
export type AttentionRowView = { id: string; label: string; value: number | null; filter?: string; color: string; hint: string };
export type MaintenanceAttention = { maintenanceDue: number; maintenanceDueSoon: number; inspectionDue: number; inspectionUnknown: number; openWorkOrders: number } | null;
const props = defineProps<{ rows: AttentionRowView[]; state: HomeDataState; active: string; compact: boolean; demo: boolean; note?: string; maintenance?: MaintenanceAttention; maintenanceError?: boolean }>();
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
    <template v-if="maintenance">
      <RouterLink class="attention-row" to="/maintenance" data-attention="maintenance-due" :class="{ zero: maintenance.maintenanceDue === 0 }"
        :aria-label="`维护到期或逾期 ${maintenance.maintenanceDue}`" title="已批准规则中到期或逾期的真实维护项（不含模拟）">
        <i style="background: #ff9f43"></i><span>维护到期 / 逾期</span><b>{{ maintenance.maintenanceDue }}</b>
      </RouterLink>
      <RouterLink class="attention-row" to="/maintenance" data-attention="work-orders" :class="{ zero: maintenance.openWorkOrders === 0 }"
        :aria-label="`维护工单 ${maintenance.openWorkOrders}`" title="未完成的真实维护工单">
        <i style="background: #6ecdf2"></i><span>维护工单</span><b>{{ maintenance.openWorkOrders }}</b>
      </RouterLink>
      <RouterLink class="attention-row" to="/maintenance" data-attention="inspection" :class="{ zero: maintenance.inspectionDue === 0 }"
        :aria-label="`检验到期 ${maintenance.inspectionDue}，未登记 ${maintenance.inspectionUnknown}`" title="法定检验 30 天内到期或逾期；未登记有效期的资产单独计数">
        <i style="background: #e8b75d"></i><span>检验到期<small v-if="maintenance.inspectionUnknown"> · 未登记 {{ maintenance.inspectionUnknown }}</small></span><b>{{ maintenance.inspectionDue }}</b>
      </RouterLink>
    </template>
    <template v-else>
      <div class="attention-row unavailable" data-attention="work-orders" :title="maintenanceError ? '维护数据暂不可用' : '维护工单尚未接入'" :aria-label="`维护工单 ${maintenanceError ? '不可用' : '未接入'}`">
        <i></i><span>维护工单</span><b>{{ maintenanceError ? '不可用' : '未接入' }}</b>
      </div>
      <div class="attention-row unavailable" data-attention="inspection" :title="maintenanceError ? '检验数据暂不可用' : '检验到期尚未接入'" :aria-label="`检验到期 ${maintenanceError ? '不可用' : '未接入'}`">
        <i></i><span>检验到期</span><b>{{ maintenanceError ? '不可用' : '未接入' }}</b>
      </div>
    </template>
    <p v-if="!compact" class="attention-note">{{ note ?? '点击条目或指标卡即过滤地图与列表，再次点击取消。' }}</p>
  </aside>
</template>
