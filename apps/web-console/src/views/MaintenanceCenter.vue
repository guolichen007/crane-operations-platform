<script setup lang="ts">
// R9 B6 — 维护与到期中心: every due row of the authorized projects (rule schedules + statutory
// inspection), alerts and work orders; filters by project / asset / mechanism / component /
// status / data source. Real API only; simulated rows are badged and filtered out by default.
import { computed, ref } from 'vue';
import { useQuery } from '@tanstack/vue-query';
import { useRouter } from 'vue-router';
import { RefreshCw } from 'lucide-vue-next';
import { getCenter, filterCenter, sourceModeBadge, statusView, daysLeftText, CATEGORY_TEXT, type CenterFilter, type CenterRow } from '../cloud/maintenance';
import { zh, errorZh, equipmentTypeName, STAGE_ZH, WO_STATUS_ZH, APPROVAL_ZH } from '../cloud/zh';
import { formatCloudTime as fmt } from '../cloud/format';
const formatCloudTime = (v: string | null | undefined) => fmt(v ?? undefined);

const router = useRouter();
const center = useQuery({ queryKey: ['r9-center'], queryFn: getCenter, refetchInterval: 60_000 });
const f = ref<CenterFilter>({ project: '', asset: '', mechanism: '', slot: '', status: '', mode: 'REAL' });
const rows = computed(() => center.data.value?.rows ?? []);
const uniq = (vals: (string | null)[]) => [...new Set(vals.filter((v): v is string => !!v))].sort();
const projects = computed(() => uniq(rows.value.map((r) => r.projectId)).map((id) => ({ id, name: rows.value.find((r) => r.projectId === id)?.projectName ?? id })));
const assets = computed(() => uniq(rows.value.filter((r) => !f.value.project || r.projectId === f.value.project).map((r) => r.assetId)).map((id) => ({ id, name: rows.value.find((r) => r.assetId === id)?.assetName ?? id })));
const mechanisms = computed(() => uniq(rows.value.filter((r) => !f.value.asset || r.assetId === f.value.asset).map((r) => r.mechanism)));
const slots = computed(() => uniq(rows.value.filter((r) => (!f.value.asset || r.assetId === f.value.asset) && (!f.value.mechanism || r.mechanism === f.value.mechanism)).map((r) => r.equipmentSlot)));
const slotLabel = (slot: string) => {
  const r = rows.value.find((x) => x.equipmentSlot === slot && x.equipmentName);
  return r ? `${r.mechanism ?? ''} · ${r.equipmentName}` : `器件 ${slot}`;
};
const deviceName = (r: CenterRow) => (r.equipmentName ? r.equipmentName : r.equipmentSlot ? `器件 ${r.equipmentSlot}` : '整机');
const shown = computed(() => filterCenter(rows.value, f.value));
const rank: Record<string, number> = { OVERDUE: 0, DUE: 1, DUE_SOON: 2, INSUFFICIENT_DATA: 3, DRAFT: 4, NORMAL: 5 };
const sorted = computed(() => [...shown.value].sort((a, b) => (rank[a.status] ?? 9) - (rank[b.status] ?? 9) || (a.daysLeft ?? 1e9) - (b.daysLeft ?? 1e9)));
const att = computed(() => center.data.value?.attention);
const modeOk = (m: string) => f.value.mode === 'ALL' || (f.value.mode === 'REAL' ? m === 'REAL' : m !== 'REAL');
const alerts = computed(() => (center.data.value?.alerts ?? []).filter((a) => a.status === 'OPEN' && modeOk(a.sourceMode) && (!f.value.asset || a.assetId === f.value.asset)));
const orders = computed(() => (center.data.value?.workOrders ?? []).filter((w) => w.status !== 'COMPLETED' && w.status !== 'CANCELLED' && modeOk(w.sourceMode) && (!f.value.asset || w.assetId === f.value.asset)));
const errorText = (e: unknown) => errorZh(e, 'load');
const open = (assetId: string) => router.push(`/assets/${encodeURIComponent(assetId)}`);
</script>
<template>
  <section class="workspace-page maintenance-center" data-testid="maintenance-center">
    <header class="workspace-page-head">
      <div>
        <h1>维护与到期中心</h1>
        <p>维护、校准、检查、寿命与法定检验到期统一视图 · 系统提醒不替代法定检验结论</p>
      </div>
      <button class="ghost-action" aria-label="刷新" @click="center.refetch()"><RefreshCw :size="14" /></button>
    </header>
    <p v-if="center.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></p>
    <p v-else-if="center.isError.value" class="workspace-error" role="alert">{{ errorText(center.error.value) }}</p>
    <template v-else-if="center.data.value">
      <div class="mc-kpis" data-testid="mc-kpis">
        <div class="mc-kpi" data-kpi="due"><b>{{ att?.maintenanceDue }}</b><span>到期 / 逾期（真实）</span></div>
        <div class="mc-kpi" data-kpi="soon"><b>{{ att?.maintenanceDueSoon }}</b><span>即将到期（真实）</span></div>
        <div class="mc-kpi" data-kpi="inspection"><b>{{ att?.inspectionDue }}</b><span>法定检验 30 天内到期</span></div>
        <div class="mc-kpi" data-kpi="insufficient"><b>{{ att?.insufficientData }}</b><span>数据不足</span></div>
        <div class="mc-kpi" data-kpi="orders"><b>{{ att?.openWorkOrders }}</b><span>未完成工单（真实）</span></div>
        <div class="mc-kpi" data-kpi="draft"><b>{{ att?.draftRules }}</b><span>待审核规则</span></div>
        <div v-if="att && (att.simulationDue || att.simulationWorkOrders)" class="mc-kpi sim" data-kpi="sim"><b>{{ att.simulationDue }} / {{ att.simulationWorkOrders }}</b><span>模拟：到期 / 工单（不计入真实）</span></div>
      </div>
      <div class="asset-filters mc-filters">
        <select v-model="f.project" aria-label="项目" @change="f.asset = ''; f.mechanism = ''; f.slot = ''"><option value="">全部项目</option><option v-for="p in projects" :key="p.id" :value="p.id">{{ p.name }}</option></select>
        <select v-model="f.asset" aria-label="行车" @change="f.mechanism = ''; f.slot = ''"><option value="">全部行车</option><option v-for="a in assets" :key="a.id" :value="a.id">{{ a.name }}</option></select>
        <select v-model="f.mechanism" aria-label="机构" @change="f.slot = ''"><option value="">全部机构</option><option v-for="m in mechanisms" :key="m" :value="m">{{ m }}</option></select>
        <select v-model="f.slot" aria-label="器件"><option value="">全部器件</option><option v-for="s in slots" :key="s" :value="s">{{ slotLabel(s) }}</option></select>
        <select v-model="f.status" aria-label="状态"><option value="">全部状态</option><option v-for="s in ['OVERDUE', 'DUE', 'DUE_SOON', 'NORMAL', 'INSUFFICIENT_DATA', 'DRAFT']" :key="s" :value="s">{{ statusView(s).text }}</option></select>
        <select v-model="f.mode" aria-label="数据来源" data-testid="mc-mode"><option value="REAL">仅真实</option><option value="SIM">仅模拟 / 测试</option><option value="ALL">全部</option></select>
      </div>
      <p v-if="f.mode !== 'REAL'" class="r9-banner sim">当前显示含<b>模拟 / 测试</b>数据，仅用于验证预警链路。</p>
      <div class="data-table-wrap">
        <table class="data-table stack-table" data-testid="mc-rows">
          <thead><tr><th>器件</th><th>项目 / 行车</th><th>类别</th><th>规则</th><th>状态</th><th>到期</th><th>工单</th></tr></thead>
          <tbody>
            <tr v-for="(r, i) in sorted" :key="i" class="data-row" tabindex="0" :data-asset="r.assetId" :data-status="r.status" :data-mode="r.sourceMode" @click="open(r.assetId)" @keyup.enter="open(r.assetId)">
              <td data-label="器件"><b>{{ deviceName(r) }}</b><div class="cell-sub">{{ r.mechanism ?? '整机' }}{{ r.equipmentType ? ' · ' + equipmentTypeName(r.equipmentType) : '' }}</div></td>
              <td data-label="项目 / 行车">{{ r.projectName }}<div class="cell-sub">{{ r.assetName }}{{ r.assetCode && r.assetCode !== r.assetName ? '（' + r.assetCode + '）' : '' }}</div></td>
              <td data-label="类别">{{ zh(CATEGORY_TEXT, r.category, '其他到期项') }}</td>
              <td data-label="规则">{{ r.ruleName }}<span v-if="r.sourceMode !== 'REAL'" class="r9-tag sim">{{ sourceModeBadge(r.sourceMode).text }}</span><div v-if="r.approvalStatus && r.approvalStatus !== 'SOURCE'" class="cell-sub">{{ zh(APPROVAL_ZH, r.approvalStatus) }}</div></td>
              <td data-label="状态"><span class="r9-status" :data-tone="statusView(r.status).tone">{{ statusView(r.status).text }}</span><div v-if="r.progressPercent !== null" class="cell-sub">使用量 {{ r.progressPercent }}%</div></td>
              <td data-label="到期">{{ r.nextDueAt ? formatCloudTime(r.nextDueAt) : '—' }}<div class="cell-sub">{{ daysLeftText(r.daysLeft) }}</div></td>
              <td data-label="工单">{{ r.openWorkOrders || '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="!sorted.length" class="workspace-empty" data-testid="mc-empty">没有符合条件的到期项。</p>
      <div class="mc-split">
        <section class="r9-card" data-testid="mc-alerts">
          <h3>待处理预警 <small>{{ alerts.length }}</small></h3>
          <ul class="r9-issues"><li v-for="a in alerts" :key="a.id" :data-stage="a.stage"><span class="r9-tag" :class="sourceModeBadge(a.sourceMode).tone">{{ sourceModeBadge(a.sourceMode).text }}</span><b>{{ a.equipmentName ?? '整机' }}</b><span class="mc-stage">{{ zh(STAGE_ZH, a.stage, '其他提醒') }}</span><small>{{ a.mechanism ? a.mechanism + ' · ' : '' }}{{ a.ruleName }} · {{ formatCloudTime(a.triggeredAt) }}</small><button class="ghost-action" @click="open(a.assetId)">前往资产</button></li></ul>
        </section>
        <section class="r9-card" data-testid="mc-orders">
          <h3>未完成工单 <small>{{ orders.length }}</small></h3>
          <ul class="r9-issues"><li v-for="w in orders" :key="w.id"><span class="r9-tag" :class="sourceModeBadge(w.sourceMode).tone">{{ sourceModeBadge(w.sourceMode).text }}</span><b>{{ w.code }}</b><small>{{ w.title }} · {{ zh(WO_STATUS_ZH, w.status) }} · {{ w.assignedTo ?? '未分配' }}</small><button class="ghost-action" @click="open(w.assetId)">前往资产</button></li></ul>
        </section>
      </div>
      <p class="cell-sub">生成于 {{ formatCloudTime(center.data.value.generatedAt) }}</p>
    </template>
  </section>
</template>
<style scoped>
.mc-kpis { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(140px, 100%), 1fr)); gap: 10px; }
.mc-kpi { border: 1px solid var(--line-soft); border-radius: 10px; padding: 10px 12px; display: flex; flex-direction: column; gap: 2px; }
.mc-kpi b { font-size: 22px; color: var(--text-1); }
.mc-kpi span { font-size: 12px; color: var(--text-3); }
.mc-kpi.sim { border-color: rgba(255, 159, 67, 0.6); }
.mc-kpi.sim b { color: #ff9f43; }
.mc-filters { flex-wrap: wrap; }
.mc-stage { color: #ff9f43; font-weight: 600; font-size: 13px; }
.mc-split { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(300px, 100%), 1fr)); gap: 12px; }
.r9-card { border: 1px solid var(--line-soft); border-radius: 12px; padding: 12px 14px; display: flex; flex-direction: column; gap: 8px; min-width: 0; max-width: 100%; box-sizing: border-box; }
.r9-card h3 { margin: 0; font-size: 14px; color: var(--text-1); }
.r9-card h3 small { color: var(--text-3); }
.r9-issues { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
.r9-issues li { display: flex; flex-wrap: wrap; gap: 4px 8px; align-items: baseline; font-size: 13px; color: var(--text-2); border-bottom: 1px dashed var(--line-soft); padding-bottom: 6px; }
.r9-issues small { color: var(--text-3); overflow-wrap: anywhere; }
.r9-tag { display: inline-block; font-size: 11px; line-height: 18px; padding: 0 6px; border-radius: 9px; border: 1px solid currentColor; margin: 0 4px; white-space: nowrap; }
.r9-tag.sim { color: #ff9f43; background: rgba(255, 159, 67, 0.12); font-weight: 600; }
.r9-tag.real { color: #55d6a4; }
.r9-status[data-tone='ok'] { color: #55d6a4; }
.r9-status[data-tone='warn'] { color: #e8b75d; }
.r9-status[data-tone='due'] { color: #ff9f43; font-weight: 600; }
.r9-status[data-tone='over'] { color: #f08a8a; font-weight: 600; }
.r9-status[data-tone='none'] { color: var(--text-3); }
.r9-banner { margin: 0; padding: 8px 12px; border-radius: 8px; border: 1px solid var(--line-soft); color: var(--text-2); font-size: 13px; }
.r9-banner.sim { border-color: rgba(255, 159, 67, 0.6); background: rgba(255, 159, 67, 0.1); color: #ffcf9e; }
@media (max-width: 700px) {
  .stack-table, .stack-table tbody, .stack-table tr, .stack-table td { display: block; width: 100%; }
  .stack-table thead { display: none; }
  .stack-table tr { border-bottom: 1px solid var(--line-soft); padding: 8px 0; }
  .stack-table td { position: relative; padding: 3px 0 3px 92px; border: 0; white-space: normal; overflow-wrap: anywhere; }
  .stack-table td::before { content: attr(data-label); position: absolute; left: 0; top: 3px; width: 84px; color: var(--text-3); font-size: 12px; }
  .mc-filters select { flex: 1 1 140px; min-width: 0; }
  .mc-kpis { grid-template-columns: 1fr 1fr; }
  .mc-kpi b { font-size: 18px; }
}
</style>
