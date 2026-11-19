<script setup lang="ts">
// R9 B6 — first-asset tabs inside 资产详情: 台账与点位 / 运行监测 / 寿命与维护 / 历史.
// Real business APIs only (no static numbers). SIMULATION / TEST rows carry a loud badge; a REAL
// value is never shown when only simulated data exists (the cell then says 「无真实数据」).
import { computed, ref } from 'vue';
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query';
import {
  listPoints, listIssues, listPending, listMeters, listReadings, listRules, listAlerts, listWorkOrders, listHistory,
  createWorkOrder, transitionWorkOrder, resolveIssue,
  sourceModeBadge, statusView, ruleStatusText, daysLeftText, formatNumber, groupByMechanism,
  STAGE_TEXT, RULE_TYPE_TEXT, KIND_TEXT, APPROVAL_TEXT, WO_STATUS_TEXT, WO_TYPE_TEXT, METRIC_TEXT, ADDRESS_KIND_TEXT, CONFIRM_TEXT,
  ISSUE_TEXT, PENDING_TEXT, HISTORY_TEXT, ROPE_CONCLUSION_TEXT,
  type WorkOrder, type MeterView, type Transition,
} from '../cloud/maintenance';
import { formatCloudTime as fmt } from '../cloud/format';
const formatCloudTime = (v: string | null | undefined) => fmt(v ?? undefined);
import { ApiError } from '../cloud/client';

const props = defineProps<{ assetId: string; tab: 'ledger' | 'monitor' | 'maintenance' | 'history'; canMaintain: boolean }>();
const queryClient = useQueryClient();
const id = computed(() => props.assetId);
const on = (t: string) => computed(() => props.tab === t);
const points = useQuery({ queryKey: computed(() => ['r9-points', id.value]), queryFn: () => listPoints(id.value), enabled: on('ledger') });
const issues = useQuery({ queryKey: computed(() => ['r9-issues', id.value]), queryFn: () => listIssues(id.value), enabled: on('ledger') });
const pending = useQuery({ queryKey: computed(() => ['r9-pending', id.value]), queryFn: () => listPending(id.value), enabled: on('ledger') });
const meters = useQuery({ queryKey: computed(() => ['r9-meters', id.value]), queryFn: () => listMeters(id.value), enabled: computed(() => props.tab === 'monitor' || props.tab === 'maintenance'), refetchInterval: 15_000 });
const rules = useQuery({ queryKey: computed(() => ['r9-rules', id.value]), queryFn: () => listRules(id.value), enabled: on('maintenance'), refetchInterval: 30_000 });
const alerts = useQuery({ queryKey: computed(() => ['r9-alerts', id.value]), queryFn: () => listAlerts(id.value), enabled: on('maintenance'), refetchInterval: 30_000 });
const orders = useQuery({ queryKey: computed(() => ['r9-orders', id.value]), queryFn: () => listWorkOrders(id.value), enabled: on('maintenance') });
const history = useQuery({ queryKey: computed(() => ['r9-history', id.value]), queryFn: () => listHistory(id.value), enabled: on('history') });

const refreshMaintenance = () => Promise.all(['r9-rules', 'r9-alerts', 'r9-orders', 'r9-meters', 'r9-history', 'r9-center'].map((k) => queryClient.invalidateQueries({ queryKey: [k] })));
const errorText = (e: unknown) => (e instanceof ApiError ? (e.status === 404 ? '无权查看或不存在' : e.message) : '网络错误，请稍后重试');

// ---------------------------------------------------------------- 台账与点位
const issueFilter = ref<'OPEN' | 'ALL'>('OPEN');
const shownIssues = computed(() => (issues.data.value ?? []).filter((i) => issueFilter.value === 'ALL' || i.status === 'OPEN'));
const grouped = computed(() => groupByMechanism(points.data.value ?? []));
const resolving = ref<{ id: string; status: 'RESOLVED' | 'ACCEPTED'; text: string } | null>(null);
const resolveMut = useMutation({
  mutationFn: () => resolveIssue(id.value, resolving.value!.id, resolving.value!.status, resolving.value!.text.trim()),
  onSuccess: async () => {
    resolving.value = null;
    await queryClient.invalidateQueries({ queryKey: ['r9-issues'] });
  },
});
const meterLabel = (code: string) => METRIC_TEXT[code.split('.').pop() ?? ''] ?? code;
const pendingValue = (c: unknown) => (c === null || c === undefined ? '未知' : `候选 ${String(c)}`);

// ---------------------------------------------------------------- 运行监测
const modeFilter = ref<'ALL' | 'REAL' | 'SIM'>('ALL');
const shownMeters = computed(() => (meters.data.value ?? []).filter((m) => modeFilter.value === 'ALL' || (modeFilter.value === 'REAL' ? m.sourceMode === 'REAL' : m.sourceMode && m.sourceMode !== 'REAL')));
const hasReal = computed(() => (meters.data.value ?? []).some((m) => m.sourceMode === 'REAL'));
const hasSim = computed(() => (meters.data.value ?? []).some((m) => m.sourceMode && m.sourceMode !== 'REAL'));
const selectedMeter = ref<MeterView | null>(null);
const readings = useQuery({
  queryKey: computed(() => ['r9-readings', id.value, selectedMeter.value?.id]),
  queryFn: () => listReadings(id.value, selectedMeter.value!.id),
  enabled: computed(() => !!selectedMeter.value),
});

// ---------------------------------------------------------------- 寿命与维护
const ruleFilter = ref<'ACTIVE' | 'DRAFT' | 'ALL'>('ACTIVE');
const shownRules = computed(() =>
  (rules.data.value ?? []).filter((r) => ruleFilter.value === 'ALL' || (ruleFilter.value === 'DRAFT' ? r.approvalStatus === 'DRAFT_UNAPPROVED' && !r.testOnly : r.approvalStatus === 'APPROVED' || r.testOnly)),
);
const draftCount = computed(() => (rules.data.value ?? []).filter((r) => r.approvalStatus === 'DRAFT_UNAPPROVED' && !r.testOnly).length);
const openAlerts = computed(() => (alerts.data.value ?? []).filter((a) => a.status === 'OPEN'));
const actionError = ref('');
const notice = ref('');
const openWO = useMutation({
  mutationFn: (alertId: string) => createWorkOrder(alertId),
  onSuccess: async (w) => {
    notice.value = `已生成工单 ${w.code}`;
    actionError.value = '';
    await refreshMaintenance();
  },
  onError: (e) => (actionError.value = errorText(e)),
});

// Work order dialog: assign → start → complete (with optional replacement / rope inspection).
const active = ref<WorkOrder | null>(null);
const form = ref({ assignee: '', result: 'DONE' as 'DONE' | 'DONE_WITH_FINDINGS' | 'NOT_DONE', basis: '', note: '', replace: false, serial: '', reason: '',
  rope: false, diameter: '', broken: '', finding: '', conclusion: '' });
function openOrder(w: WorkOrder) {
  active.value = w;
  actionError.value = '';
  form.value = { assignee: w.assignedTo ?? '', result: 'DONE', basis: '', note: '', replace: false, serial: '', reason: '', rope: w.type === 'INSPECTION', diameter: '', broken: '', finding: '', conclusion: '' };
}
const step = useMutation({
  mutationFn: (to: WorkOrder['status']) => {
    const w = active.value!;
    const f = form.value;
    const t: Transition = { to, note: f.note.trim() };
    if (to === 'ASSIGNED') t.assignedTo = f.assignee.trim();
    if (to === 'COMPLETED') {
      t.result = f.result;
      t.basis = f.basis.trim();
      if (f.replace) t.replacement = { serialNumber: f.serial.trim(), catalogItemId: '', reason: f.reason.trim(), initialUsage: null };
      if (f.rope) t.ropeInspection = { inspectionType: 'PERIODIC', measuredDiameterMm: f.diameter ? Number(f.diameter) : null, brokenWiresVisible: f.broken ? Number(f.broken) : null,
        wear: '', corrosion: '', deformation: '', lubrication: '', finding: f.finding.trim(), conclusion: f.conclusion, nextDueAt: null };
    }
    return transitionWorkOrder(w.id, w.version, t);
  },
  onSuccess: async (w) => {
    active.value = w.status === 'COMPLETED' || w.status === 'CANCELLED' ? null : w;
    notice.value = `工单 ${w.code}：${WO_STATUS_TEXT[w.status]}`;
    actionError.value = '';
    await refreshMaintenance();
  },
  onError: (e) => (actionError.value = errorText(e)),
});
const usageOf = (slot: string | null, mode: string | null) =>
  (meters.data.value ?? []).find((m) => m.equipmentSlot === slot && m.sourceMode === mode && m.kind === 'CUMULATIVE');
</script>
<template>
  <section class="r9-panel" :data-tab="tab">
    <!-- ============================================================ 台账与点位 -->
    <template v-if="tab === 'ledger'">
      <p v-if="points.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></p>
      <p v-else-if="points.isError.value" class="workspace-error" role="alert">{{ errorText(points.error.value) }}</p>
      <template v-else-if="points.data.value?.length">
        <section v-if="pending.data.value?.length" class="r9-card" data-testid="r9-pending">
          <h3>待确认信息 <small>来源文件未提供，系统不推断；确认后由资产管理员录入</small></h3>
          <ul class="r9-pending">
            <li v-for="p in pending.data.value" :key="p.subject + p.attribute" :data-attr="p.attribute">
              <b>{{ p.subject === 'asset' ? '整机' : p.subject }} · {{ PENDING_TEXT[p.attribute] ?? p.attribute }}</b>
              <span class="r9-tag warn">{{ pendingValue(p.candidate) }} · 待确认</span>
              <small>{{ p.basis }}</small>
            </li>
          </ul>
        </section>
        <section class="r9-card">
          <h3>设备台账与点位 <small>{{ points.data.value.length }} 个原始点位（全部待现场确认，不自动启用）</small></h3>
          <div v-for="g in grouped" :key="g.mechanism" class="r9-group" :data-mechanism="g.mechanism">
            <h4>{{ g.mechanism }} <small>{{ g.rows.length }}</small></h4>
            <div class="data-table-wrap">
              <table class="data-table stack-table r9-points" data-testid="r9-points">
                <thead><tr><th>点位</th><th>名称 / 设备</th><th>PLC 原文地址</th><th>原始类型 → 派生</th><th>单位</th><th>品牌型号 / 寿命（原文）</th><th>状态</th></tr></thead>
                <tbody>
                  <tr v-for="p in g.rows" :key="p.id" :data-point="p.pointCode">
                    <td data-label="点位"><b>{{ p.pointCode }}</b><div class="cell-sub">源行 {{ p.sourceRow }}</div></td>
                    <td data-label="名称 / 设备">{{ p.name }}<div class="cell-sub">{{ p.equipmentCode ?? '作业统计（非设备）' }}<template v-if="p.typeCode"> · {{ p.typeCode }}</template></div></td>
                    <td data-label="PLC 地址"><code>{{ p.plcAddressRaw ?? '—' }}</code><div class="cell-sub" :class="{ warn: p.addressKind === 'UNPARSED' }">{{ ADDRESS_KIND_TEXT[p.addressKind] }}</div></td>
                    <td data-label="类型">{{ p.rawDataType }} → {{ p.derivedType }}<div class="cell-sub">{{ p.meters.map((m) => meterLabel(m)).join(' / ') || '—' }}</div></td>
                    <td data-label="单位">{{ p.unitRaw ?? '—' }}<div class="cell-sub">→ {{ p.unit }}</div></td>
                    <td data-label="品牌 / 寿命">{{ p.brandModel ?? '—' }}<div class="cell-sub">{{ p.lifeText ?? '—' }}</div></td>
                    <td data-label="状态"><span class="r9-tag warn">{{ CONFIRM_TEXT[p.confirmationStatus] ?? p.confirmationStatus }}</span><span v-if="p.openIssues" class="r9-tag over">{{ p.openIssues }} 项待核</span></td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </section>
        <section class="r9-card" data-testid="r9-issues">
          <h3>点位核查项
            <select v-model="issueFilter" aria-label="核查项筛选"><option value="OPEN">未处理</option><option value="ALL">全部</option></select>
          </h3>
          <ul class="r9-issues">
            <li v-for="i in shownIssues" :key="i.id" :data-issue="i.issueCode" :data-status="i.status">
              <span class="r9-tag" :class="i.status === 'OPEN' ? 'over' : 'ok'">{{ i.status === 'OPEN' ? '待处理' : i.status === 'RESOLVED' ? '已处理' : '已接受' }}</span>
              <b>{{ ISSUE_TEXT[i.issueCode] ?? i.issueCode }}</b>
              <small>{{ i.pointCode ?? '' }}{{ i.sourceRow ? ' · 源行 ' + i.sourceRow : '' }}{{ (i.detail as Record<string, unknown>).plcAddress ? ' · ' + (i.detail as Record<string, unknown>).plcAddress : '' }}</small>
              <small v-if="i.resolution">{{ i.resolvedBy }}：{{ i.resolution }}</small>
              <button v-if="canMaintain && i.status === 'OPEN'" class="ghost-action" @click="resolving = { id: i.id, status: 'RESOLVED', text: '' }">记录处理结果</button>
            </li>
          </ul>
        </section>
      </template>
      <p v-else class="workspace-empty" data-testid="r9-no-points">该资产没有导入的点位清单。</p>
    </template>

    <!-- ============================================================ 运行监测 -->
    <template v-else-if="tab === 'monitor'">
      <p v-if="hasSim" class="r9-banner sim" data-testid="r9-sim-banner">⚠ 以下含<b>模拟数据</b>（SIMULATION / TEST），仅用于验证计量与预警链路，不计入真实使用量、维护与合规记录。真实机械控制关闭。</p>
      <p v-if="!hasReal" class="r9-banner" data-testid="r9-no-real">尚无真实设备接入（真实 PLC 未授权连接），真实计量为空。</p>
      <div class="r9-toolbar">
        <select v-model="modeFilter" aria-label="数据来源"><option value="ALL">全部来源</option><option value="REAL">仅真实</option><option value="SIM">仅模拟/测试</option></select>
      </div>
      <p v-if="meters.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></p>
      <div v-else class="data-table-wrap">
        <table class="data-table stack-table" data-testid="r9-meters">
          <thead><tr><th>指标</th><th>来源</th><th>累计 / 当日</th><th>器件使用量</th><th>最近采样</th><th>质量</th></tr></thead>
          <tbody>
            <tr v-for="m in shownMeters" :key="m.id + (m.sourceMode ?? '')" :data-meter="m.meterCode" :data-mode="m.sourceMode ?? 'NONE'" class="data-row" tabindex="0" @click="selectedMeter = m" @keyup.enter="selectedMeter = m">
              <td data-label="指标"><b>{{ m.pointCode }}</b> {{ METRIC_TEXT[m.metric] ?? m.metric }}<div class="cell-sub">{{ m.formula }}</div></td>
              <td data-label="来源"><span class="r9-tag" :class="sourceModeBadge(m.sourceMode).tone" :data-mode-badge="m.sourceMode ?? ''">{{ sourceModeBadge(m.sourceMode).text }}</span></td>
              <td data-label="累计 / 当日">{{ m.kind === 'DAILY' ? formatNumber(m.today, m.unit) : formatNumber(m.accumulated, m.unit) }}<div v-if="m.status !== 'ACTIVE'" class="cell-sub warn">{{ m.status === 'UNIT_CONFLICT_PENDING' ? '单位冲突待确认' : '口径待审核' }}</div></td>
              <td data-label="器件使用量">{{ m.equipmentUsage === null ? (m.kind === 'CUMULATIVE' && m.sourceMode ? '数据不足（无基准或初始使用量未知）' : '—') : formatNumber(m.equipmentUsage, m.unit) }}
                <div v-if="m.trustState === 'DECREASE_PENDING'" class="cell-sub warn" data-testid="r9-meter-pending">计数低于可信累计（原始值 {{ formatNumber(m.pendingRawValue ?? null) }}），未计入，待恢复或管理员确认复位</div></td>
              <td data-label="最近采样">{{ formatCloudTime(m.lastSampledAt) }}</td>
              <td data-label="质量">{{ m.lastQuality ?? '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <section v-if="selectedMeter" class="r9-card" data-testid="r9-readings">
        <h3>{{ selectedMeter.pointCode }} {{ METRIC_TEXT[selectedMeter.metric] }} · 最近 50 条读数 <button class="ghost-action" @click="selectedMeter = null">关闭</button></h3>
        <table class="data-table stack-table">
          <thead><tr><th>采样时间</th><th>来源</th><th>原始值</th><th>增量</th><th>累计</th><th>异常</th></tr></thead>
          <tbody>
            <tr v-for="(r, i) in readings.data.value ?? []" :key="i">
              <td data-label="时间">{{ formatCloudTime(r.sampledAt) }}</td>
              <td data-label="来源"><span class="r9-tag" :class="sourceModeBadge(r.sourceMode).tone">{{ sourceModeBadge(r.sourceMode).text }}</span></td>
              <td data-label="原始值">{{ formatNumber(r.rawValue) }}</td>
              <td data-label="增量">{{ formatNumber(r.delta) }}</td>
              <td data-label="累计">{{ formatNumber(r.accumulated) }}</td>
              <td data-label="异常">{{ r.anomaly ?? '—' }}</td>
            </tr>
          </tbody>
        </table>
      </section>
    </template>

    <!-- ============================================================ 寿命与维护 -->
    <template v-else-if="tab === 'maintenance'">
      <p v-if="notice" class="workspace-notice" role="status" data-testid="r9-notice">{{ notice }}</p>
      <p v-if="actionError" class="form-error" role="alert" data-testid="r9-error">{{ actionError }}</p>
      <p class="r9-banner">寿命与维护提醒只作为提示；不替代法定检验结论，系统不自动判定报废。原始清单中的寿命为<b>待审核初始规则</b>（{{ draftCount }} 条），审核批准并填写生效日期后才参与真实提醒。</p>
      <div class="r9-toolbar">
        <select v-model="ruleFilter" aria-label="规则筛选"><option value="ACTIVE">已生效（含模拟测试规则）</option><option value="DRAFT">待审核</option><option value="ALL">全部</option></select>
      </div>
      <div class="data-table-wrap">
        <table class="data-table stack-table" data-testid="r9-rules">
          <thead><tr><th>规则</th><th>类型</th><th>周期</th><th>状态</th><th>下次到期</th><th>依据</th></tr></thead>
          <tbody>
            <tr v-for="r in shownRules" :key="r.id + (r.sourceMode ?? '')" :data-rule="r.ruleCode" :data-mode="r.sourceMode ?? ''">
              <td data-label="规则"><b>{{ r.name }}</b><div class="cell-sub">{{ r.ruleCode }} · {{ KIND_TEXT[r.kind] ?? r.kind }}<template v-if="r.equipmentSlot"> · {{ r.equipmentSlot }}</template></div>
                <span v-if="r.testOnly" class="r9-tag sim">模拟测试规则</span><span v-else class="r9-tag" :class="r.approvalStatus === 'APPROVED' ? 'ok' : 'warn'">{{ APPROVAL_TEXT[r.approvalStatus] }}</span></td>
              <td data-label="类型">{{ RULE_TYPE_TEXT[r.type] }}</td>
              <td data-label="周期">{{ r.intervalDays ? r.intervalDays + ' 天' : '' }}{{ r.intervalDays && r.intervalMeter ? ' / ' : '' }}{{ r.intervalMeter ? formatNumber(r.intervalMeter) : '' }}{{ !r.intervalDays && !r.intervalMeter ? '待定义' : '' }}</td>
              <td data-label="状态"><span class="r9-status" :data-tone="statusView(r.status ?? (r.approvalStatus === 'APPROVED' ? 'INSUFFICIENT_DATA' : 'DRAFT')).tone" :data-status="r.status ?? ''" :data-gaps="(r.dataGaps ?? []).join(',')">{{ ruleStatusText(r) }}</span>
                <div v-if="r.progressPercent !== null" class="cell-sub">使用量 {{ r.progressPercent }}%</div>
                <div v-if="r.reason" class="cell-sub warn" data-testid="r9-rule-reason">{{ r.reason }}</div></td>
              <td data-label="下次到期">{{ r.nextDueAt ? formatCloudTime(r.nextDueAt) : '—' }}<div class="cell-sub">{{ daysLeftText(r.daysLeft) }}{{ r.cycleNo ? ' · 第 ' + r.cycleNo + ' 周期' : '' }}</div></td>
              <td data-label="依据"><small>{{ r.basis }}</small></td>
            </tr>
          </tbody>
        </table>
      </div>
      <section class="r9-card" data-testid="r9-alerts">
        <h3>预警 <small>{{ openAlerts.length }} 条待处理</small></h3>
        <ul class="r9-issues">
          <li v-for="a in alerts.data.value ?? []" :key="a.id" :data-stage="a.stage" :data-mode="a.sourceMode" :data-alert-status="a.status">
            <span class="r9-tag" :class="sourceModeBadge(a.sourceMode).tone">{{ sourceModeBadge(a.sourceMode).text }}</span>
            <b>{{ a.ruleName }} · {{ STAGE_TEXT[a.stage] ?? a.stage }}</b>
            <small>第 {{ a.cycleNo }} 周期 · {{ formatCloudTime(a.triggeredAt) }} · {{ a.status === 'OPEN' ? '待处理' : a.status === 'WORK_ORDER' ? '已生成工单' : '已关闭' }}</small>
            <button v-if="canMaintain && a.status === 'OPEN'" class="ghost-action" :disabled="openWO.isPending.value" data-testid="r9-create-wo" @click="openWO.mutate(a.id)">生成工单</button>
          </li>
        </ul>
      </section>
      <section class="r9-card" data-testid="r9-orders">
        <h3>维护工单</h3>
        <div class="data-table-wrap">
          <table class="data-table stack-table">
            <thead><tr><th>工单</th><th>类型</th><th>状态</th><th>责任人</th><th>计划 / 完成</th><th></th></tr></thead>
            <tbody>
              <tr v-for="w in orders.data.value ?? []" :key="w.id" :data-wo="w.code" :data-wo-status="w.status">
                <td data-label="工单"><b>{{ w.code }}</b> <span class="r9-tag" :class="sourceModeBadge(w.sourceMode).tone">{{ sourceModeBadge(w.sourceMode).text }}</span><div class="cell-sub">{{ w.title }}</div></td>
                <td data-label="类型">{{ WO_TYPE_TEXT[w.type] ?? w.type }}<div class="cell-sub">{{ w.equipmentSlot ?? '整机' }}</div></td>
                <td data-label="状态">{{ WO_STATUS_TEXT[w.status] }}<div v-if="w.result" class="cell-sub">{{ w.result }}</div></td>
                <td data-label="责任人">{{ w.assignedTo ?? '—' }}</td>
                <td data-label="时间">{{ formatCloudTime(w.createdAt) }}<div class="cell-sub">{{ w.completedAt ? '完成 ' + formatCloudTime(w.completedAt) + ' · ' + (w.completedBy ?? '') : '' }}</div></td>
                <td data-label="操作"><button v-if="canMaintain && w.status !== 'COMPLETED' && w.status !== 'CANCELLED'" class="ghost-action" data-testid="r9-wo-open" @click="openOrder(w)">处理</button>
                  <small v-else-if="w.basis">{{ w.basis }}</small></td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-if="!(orders.data.value ?? []).length" class="workspace-empty">暂无工单。</p>
      </section>
    </template>

    <!-- ============================================================ 历史 -->
    <template v-else>
      <p v-if="history.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></p>
      <ol v-else class="r9-history" data-testid="r9-history">
        <li v-for="(h, i) in history.data.value ?? []" :key="i" :data-category="h.category" :data-mode="h.sourceMode">
          <span class="h-time">{{ formatCloudTime(h.at) }}</span>
          <span class="h-cat">{{ HISTORY_TEXT[h.category] ?? h.category }} <span v-if="h.sourceMode !== 'REAL'" class="r9-tag sim">{{ sourceModeBadge(h.sourceMode).text }}</span></span>
          <span class="h-sum">{{ h.summary }}</span>
          <span class="h-actor">{{ h.actor }}</span>
        </li>
      </ol>
    </template>

    <!-- Work order dialog -->
    <div v-if="active" class="modal-backdrop" @click.self="!step.isPending.value && (active = null)">
      <form class="workspace-modal r9-wo" data-testid="r9-wo-dialog" novalidate @submit.prevent>
        <h2>{{ active.code }} <span class="r9-tag" :class="sourceModeBadge(active.sourceMode).tone">{{ sourceModeBadge(active.sourceMode).text }}</span></h2>
        <p class="form-hint">{{ active.title }} · {{ WO_STATUS_TEXT[active.status] }}<template v-if="active.equipmentSlot"> · {{ active.equipmentSlot }}，当前使用量 {{ formatNumber(usageOf(active.equipmentSlot, active.sourceMode)?.equipmentUsage ?? null) }}</template></p>
        <template v-if="active.status === 'OPEN' || active.status === 'ASSIGNED'">
          <label>责任人<input v-model="form.assignee" data-testid="r9-wo-assignee" maxlength="80" /></label>
          <div class="modal-actions">
            <button type="button" class="ghost-action" :disabled="step.isPending.value || !form.assignee.trim()" data-testid="r9-wo-assign" @click="step.mutate('ASSIGNED')">分配</button>
            <button v-if="active.status === 'ASSIGNED'" type="button" class="primary-action" :disabled="step.isPending.value" data-testid="r9-wo-start" @click="step.mutate('IN_PROGRESS')">开始执行</button>
          </div>
        </template>
        <template v-if="active.status === 'IN_PROGRESS'">
          <label>结果<select v-model="form.result" data-testid="r9-wo-result"><option value="DONE">已完成</option><option value="DONE_WITH_FINDINGS">完成（有发现）</option><option value="NOT_DONE">未完成</option></select></label>
          <label>完成依据 / 工作内容 *<input v-model="form.basis" data-testid="r9-wo-basis" maxlength="200" /></label>
          <label class="check-row"><input v-model="form.replace" type="checkbox" data-testid="r9-wo-replace" />本次更换了器件（生成新的安装实例并重建计量基准，原始累计量不变）</label>
          <template v-if="form.replace">
            <label>新器件序列号<input v-model="form.serial" maxlength="120" /></label>
            <label>更换原因 *<input v-model="form.reason" data-testid="r9-wo-reason" maxlength="200" /></label>
          </template>
          <template v-if="form.rope">
            <fieldset class="r9-rope"><legend>钢丝绳检查（结论由检查人填写，系统不判定报废）</legend>
              <label>实测直径 mm<input v-model="form.diameter" inputmode="decimal" /></label>
              <label>可见断丝数<input v-model="form.broken" inputmode="numeric" /></label>
              <label>发现<input v-model="form.finding" maxlength="200" /></label>
              <label>检查结论 *<select v-model="form.conclusion" data-testid="r9-rope-conclusion"><option value="" disabled>请选择</option><option v-for="(t, k) in ROPE_CONCLUSION_TEXT" :key="k" :value="k">{{ t }}</option></select></label>
            </fieldset>
          </template>
          <div class="modal-actions">
            <button type="button" class="primary-action" :disabled="step.isPending.value || !form.basis.trim() || (form.replace && !form.reason.trim()) || (form.rope && !form.conclusion)" data-testid="r9-wo-complete" @click="step.mutate('COMPLETED')">完成工单</button>
          </div>
        </template>
        <label>备注<input v-model="form.note" maxlength="200" /></label>
        <p v-if="actionError" class="form-error" role="alert">{{ actionError }}</p>
        <div class="modal-actions">
          <button type="button" class="danger-action" :disabled="step.isPending.value || !form.note.trim()" title="取消需填写备注" @click="step.mutate('CANCELLED')">取消工单</button>
          <button type="button" class="ghost-action" :disabled="step.isPending.value" @click="active = null">关闭</button>
        </div>
      </form>
    </div>
    <div v-if="resolving" class="modal-backdrop" @click.self="resolving = null">
      <form class="workspace-modal" data-testid="r9-resolve-dialog" @submit.prevent="resolveMut.mutate()">
        <h2>记录核查结果</h2>
        <label>结论<select v-model="resolving.status"><option value="RESOLVED">已处理（现场核实并修正）</option><option value="ACCEPTED">接受现状（说明原因）</option></select></label>
        <label>说明 *<input v-model="resolving.text" maxlength="200" /></label>
        <div class="modal-actions"><button type="button" class="ghost-action" @click="resolving = null">取消</button><button type="submit" class="primary-action" :disabled="resolveMut.isPending.value || resolving.text.trim().length < 2">保存</button></div>
      </form>
    </div>
  </section>
</template>
<style scoped>
.r9-panel { display: flex; flex-direction: column; gap: 12px; }
.r9-card { border: 1px solid var(--line-soft); border-radius: 12px; padding: 12px 14px; display: flex; flex-direction: column; gap: 8px; min-width: 0; max-width: 100%; box-sizing: border-box; }
.r9-card h3 { margin: 0; font-size: 14px; color: var(--text-1); display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.r9-card h3 small { color: var(--text-3); font-weight: 400; }
.r9-group h4 { margin: 8px 0 4px; font-size: 13px; color: var(--text-2); }
.r9-group h4 small { color: var(--text-3); }
.r9-tag { display: inline-block; font-size: 11px; line-height: 18px; padding: 0 6px; border-radius: 9px; border: 1px solid currentColor; margin-right: 4px; white-space: nowrap; }
.r9-tag.sim { color: #ff9f43; background: rgba(255, 159, 67, 0.12); font-weight: 600; }
.r9-tag.real, .r9-tag.ok { color: #55d6a4; }
.r9-tag.warn { color: #e8b75d; }
.r9-tag.over { color: #f08a8a; }
.r9-tag.none { color: var(--text-3); }
.r9-status[data-tone='ok'] { color: #55d6a4; }
.r9-status[data-tone='warn'] { color: #e8b75d; }
.r9-status[data-tone='due'] { color: #ff9f43; font-weight: 600; }
.r9-status[data-tone='over'] { color: #f08a8a; font-weight: 600; }
.r9-status[data-tone='none'] { color: var(--text-3); }
.r9-banner { margin: 0; padding: 8px 12px; border-radius: 8px; border: 1px solid var(--line-soft); color: var(--text-2); font-size: 13px; }
.r9-banner.sim { border-color: rgba(255, 159, 67, 0.6); background: rgba(255, 159, 67, 0.1); color: #ffcf9e; }
.r9-toolbar { display: flex; gap: 8px; flex-wrap: wrap; }
.r9-toolbar select, .r9-card select, .r9-wo input, .r9-wo select { height: 32px; padding: 0 8px; border: 1px solid var(--line-soft); border-radius: 7px; background: var(--surface-900); color: var(--text-1); font-size: 13px; }
.r9-pending, .r9-issues { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
.r9-pending li, .r9-issues li { display: flex; flex-wrap: wrap; gap: 4px 8px; align-items: baseline; font-size: 13px; color: var(--text-2); border-bottom: 1px dashed var(--line-soft); padding-bottom: 6px; }
.r9-pending small, .r9-issues small { color: var(--text-3); overflow-wrap: anywhere; }
.cell-sub.warn { color: #e8b75d; }
code { font-size: 12px; overflow-wrap: anywhere; }
.r9-history { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
.r9-history li { display: grid; grid-template-columns: 150px 120px minmax(0, 1fr) 140px; gap: 4px 10px; font-size: 12px; color: var(--text-2); border-bottom: 1px solid var(--line-soft); padding-bottom: 6px; }
.h-sum { overflow-wrap: anywhere; }
.r9-wo { width: min(620px, 94vw); display: flex; flex-direction: column; gap: 10px; max-height: 92vh; overflow: auto; }
.r9-wo label { display: flex; flex-direction: column; gap: 6px; font-size: 12px; color: var(--text-3); }
.r9-wo .check-row { flex-direction: row; align-items: center; gap: 8px; }
.r9-rope { border: 1px solid var(--line-soft); border-radius: 10px; padding: 10px; display: grid; gap: 8px; }
@media (max-width: 700px) {
  .stack-table, .stack-table tbody, .stack-table tr, .stack-table td { display: block; width: 100%; }
  .stack-table thead { display: none; }
  .stack-table tr { border-bottom: 1px solid var(--line-soft); padding: 8px 0; }
  .stack-table td { position: relative; padding: 3px 0 3px 88px; border: 0; white-space: normal; overflow-wrap: anywhere; }
  .stack-table td::before { content: attr(data-label); position: absolute; left: 0; top: 3px; width: 80px; color: var(--text-3); font-size: 12px; }
  .r9-history li { grid-template-columns: 1fr; }
  .r9-wo { width: 100vw; border-radius: 0; }
}
</style>
