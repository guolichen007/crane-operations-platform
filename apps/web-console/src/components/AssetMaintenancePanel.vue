<script setup lang="ts">
// R9 B6 / R9-UX — first-asset tabs inside 资产详情: 台账与点位 / 运行监测 / 寿命与维护 / 历史.
// Real business APIs only (no static numbers). SIMULATION / TEST rows carry a loud badge; a REAL
// value is never shown when only simulated data exists. All visible texts come from the shared
// Chinese module (cloud/zh.ts); codes (point / equipment / work-order numbers, PLC addresses,
// brand / model) are data and stay as-is, program enums never reach the screen.
import { computed, ref } from 'vue';
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query';
import {
  listPoints, listIssues, listPending, listMeters, listReadings, listRules, listAlerts, listWorkOrders, listHistory,
  createWorkOrder, transitionWorkOrder, resolveIssue,
  sourceModeBadge, statusView, ruleStatusText, daysLeftText, formatNumber, groupByMechanism,
  ADDRESS_KIND_TEXT, CONFIRM_TEXT,
  type WorkOrder, type MeterView, type Transition, type PointView, type RuleView, type AlertView,
} from '../cloud/maintenance';
import {
  zh, errorZh, unitZh, meterMethodZh, pendingValueZh, historySummaryZh, mechanismOfSlot,
  EQUIPMENT_TYPE_TEXT, RAW_DATA_TYPE_ZH, DERIVED_TYPE_ZH, ACQUISITION_SOURCE_ZH, METRIC_ZH, METER_STATUS_ZH, QUALITY_ZH, ANOMALY_ZH, TRUST_ZH,
  ISSUE_ZH, ISSUE_STATUS_ZH, PENDING_ATTR_ZH, STAGE_ZH, RULE_KIND_ZH, RULE_TYPE_ZH, APPROVAL_ZH, WO_STATUS_ZH, WO_TYPE_ZH, WO_RESULT_ZH,
  ALERT_STATUS_ZH, HISTORY_CATEGORY_ZH, CONCLUSION_ZH,
} from '../cloud/zh';
import { formatCloudTime as fmt } from '../cloud/format';
const formatCloudTime = (v: string | null | undefined) => fmt(v ?? undefined);

const props = defineProps<{ assetId: string; tab: 'ledger' | 'monitor' | 'maintenance' | 'history'; canMaintain: boolean }>();
const queryClient = useQueryClient();
const id = computed(() => props.assetId);
const on = (t: string) => computed(() => props.tab === t);
// Points are the name source of every tab (device names for meters, rules, alerts, orders, history).
const points = useQuery({ queryKey: computed(() => ['r9-points', id.value]), queryFn: () => listPoints(id.value) });
const issues = useQuery({ queryKey: computed(() => ['r9-issues', id.value]), queryFn: () => listIssues(id.value), enabled: on('ledger') });
const pending = useQuery({ queryKey: computed(() => ['r9-pending', id.value]), queryFn: () => listPending(id.value), enabled: on('ledger') });
const meters = useQuery({ queryKey: computed(() => ['r9-meters', id.value]), queryFn: () => listMeters(id.value), enabled: computed(() => props.tab === 'monitor' || props.tab === 'maintenance'), refetchInterval: 15_000 });
const rules = useQuery({ queryKey: computed(() => ['r9-rules', id.value]), queryFn: () => listRules(id.value), enabled: on('maintenance'), refetchInterval: 30_000 });
const alerts = useQuery({ queryKey: computed(() => ['r9-alerts', id.value]), queryFn: () => listAlerts(id.value), enabled: on('maintenance'), refetchInterval: 30_000 });
const orders = useQuery({ queryKey: computed(() => ['r9-orders', id.value]), queryFn: () => listWorkOrders(id.value), enabled: on('maintenance') });
const history = useQuery({ queryKey: computed(() => ['r9-history', id.value]), queryFn: () => listHistory(id.value), enabled: on('history') });

const refreshMaintenance = () => Promise.all(['r9-rules', 'r9-alerts', 'r9-orders', 'r9-meters', 'r9-history', 'r9-center'].map((k) => queryClient.invalidateQueries({ queryKey: [k] })));
const errorText = (e: unknown) => errorZh(e, 'load');

// ---------------------------------------------------------------- names
const pointByCode = computed(() => new Map((points.data.value ?? []).map((p) => [p.pointCode, p])));
const pointBySlot = computed(() => {
  const m = new Map<string, PointView>();
  for (const p of points.data.value ?? []) if (p.equipmentSlot && !m.has(p.equipmentSlot)) m.set(p.equipmentSlot, p);
  return m;
});
/** 「大车机构 · 总接触器」 for a slot (the slot code stays a secondary number). */
const slotName = (slot: string | null | undefined): string => {
  if (!slot) return '整机';
  const p = pointBySlot.value.get(slot);
  return p ? `${p.mechanism} · ${p.name}` : `${mechanismOfSlot(slot)} · 器件 ${slot}`;
};
const typeName = (code: string | null) => (code ? EQUIPMENT_TYPE_TEXT[code] ?? '未登记的设备类型' : '');

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
const meterLabel = (code: string) => METRIC_ZH[code.split('.').pop() ?? ''] ?? '指标';
const pendingValue = (attribute: string, c: unknown) => (c === null || c === undefined ? '未知' : `候选 ${pendingValueZh(attribute, c)}`);
const issueAddress = (d: Record<string, unknown>) => (typeof d.plcAddress === 'string' ? d.plcAddress : '');

// ---------------------------------------------------------------- 运行监测
const modeFilter = ref<'ALL' | 'REAL' | 'SIM'>('ALL');
const meterSearch = ref('');
const meterName = (m: MeterView) => pointByCode.value.get(m.pointCode)?.name ?? `点位 ${m.pointCode}`;
const meterMechanism = (m: MeterView) => pointByCode.value.get(m.pointCode)?.mechanism ?? mechanismOfSlot(m.equipmentSlot);
const meterConfirmed = (m: MeterView) => pointByCode.value.get(m.pointCode)?.confirmationStatus === 'CONFIRMED';
const shownMeters = computed(() => {
  const q = meterSearch.value.trim().toLowerCase();
  return (meters.data.value ?? []).filter((m) =>
    (modeFilter.value === 'ALL' || (modeFilter.value === 'REAL' ? m.sourceMode === 'REAL' : m.sourceMode && m.sourceMode !== 'REAL')) &&
    (!q || [meterName(m), meterMechanism(m), m.pointCode, m.meterCode, METRIC_ZH[m.metric] ?? ''].some((t) => t.toLowerCase().includes(q))));
});
const meterGroups = computed(() => {
  const out: { mechanism: string; rows: MeterView[] }[] = [];
  for (const m of shownMeters.value) {
    const name = meterMechanism(m);
    const g = out.find((x) => x.mechanism === name);
    if (g) g.rows.push(m);
    else out.push({ mechanism: name, rows: [m] });
  }
  return out;
});
const hasReal = computed(() => (meters.data.value ?? []).some((m) => m.sourceMode === 'REAL'));
const hasSim = computed(() => (meters.data.value ?? []).some((m) => m.sourceMode && m.sourceMode !== 'REAL'));
const anomalyCount = computed(() => (meters.data.value ?? []).filter((m) => m.trustState === 'DECREASE_PENDING' || (m.lastQuality && m.lastQuality !== 'GOOD')).length);
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
const ruleOf = (a: AlertView): RuleView | undefined => (rules.data.value ?? []).find((r) => r.id === a.ruleId && (r.sourceMode ?? a.sourceMode) === a.sourceMode) ?? (rules.data.value ?? []).find((r) => r.id === a.ruleId);
/** 「当前使用量 …」 and 「剩余 …」 of an alert (from the rule schedule and the slot meter). */
function alertFacts(a: AlertView): string {
  const r = ruleOf(a);
  if (!r) return '';
  const parts: string[] = [];
  const usage = usageOf(r.equipmentSlot, a.sourceMode)?.equipmentUsage;
  if (r.meterCode && usage !== undefined && usage !== null) parts.push(`当前使用量 ${formatNumber(usage, usageOf(r.equipmentSlot, a.sourceMode)?.unit)}`);
  if (r.nextDueMeter !== null && usage !== undefined && usage !== null && r.progressPercent !== null) {
    const left = (r.intervalMeter ?? 0) * (1 - r.progressPercent / 100);
    parts.push(left > 0 ? `距阈值约 ${formatNumber(left)}` : '已达计量阈值');
  }
  if (r.daysLeft !== null) parts.push(daysLeftText(r.daysLeft));
  return parts.join(' · ');
}
const actionError = ref('');
const notice = ref('');
const openWO = useMutation({
  mutationFn: (alertId: string) => createWorkOrder(alertId),
  onSuccess: async (w) => {
    notice.value = `已生成工单 ${w.code}`;
    actionError.value = '';
    await refreshMaintenance();
  },
  onError: (e) => (actionError.value = errorZh(e)),
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
    notice.value = `工单 ${w.code}：${zh(WO_STATUS_ZH, w.status)}`;
    actionError.value = '';
    await refreshMaintenance();
  },
  onError: async (e) => {
    actionError.value = errorZh(e);
    await refreshMaintenance(); // 409 / unknown outcome: read back the current state
  },
});
const usageOf = (slot: string | null, mode: string | null) =>
  (meters.data.value ?? []).find((m) => m.equipmentSlot === slot && m.sourceMode === mode && m.kind === 'CUMULATIVE');

// ---------------------------------------------------------------- 历史
const historyText = (category: string, summary: string) => {
  if (category === 'REPLACEMENT') {
    const i = summary.indexOf('：');
    return i > 0 ? `${slotName(summary.slice(0, i))}：${summary.slice(i + 1)}` : summary;
  }
  return historySummaryZh(category, summary);
};
</script>
<template>
  <section class="r9-panel" :data-tab="tab">
    <!-- ============================================================ 台账与点位 -->
    <template v-if="tab === 'ledger'">
      <p v-if="points.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></p>
      <p v-else-if="points.isError.value" class="workspace-error" role="alert">{{ errorText(points.error.value) }}</p>
      <template v-else-if="points.data.value?.length">
        <section v-if="pending.data.value?.length" class="r9-card" data-testid="r9-pending">
          <h3>待确认信息 <small>来源文件未提供，系统不推断；在「接入准备」中依据现场资料确认</small></h3>
          <ul class="r9-pending">
            <li v-for="p in pending.data.value" :key="p.subject + p.attribute" :data-attr="p.attribute">
              <b>{{ p.subject === 'asset' ? '整机' : slotName(p.subject) }} · {{ zh(PENDING_ATTR_ZH, p.attribute, '其他待确认属性') }}</b>
              <span class="r9-tag warn">{{ pendingValue(p.attribute, p.candidate) }} · 待确认</span>
              <small>{{ p.basis }}</small>
            </li>
          </ul>
        </section>
        <section class="r9-card">
          <h3>设备台账 <small>{{ points.data.value.length }} 条原始来源记录；PLC 地址、原始类型与寿命原文在每行“来源详情”中</small></h3>
          <div v-for="g in grouped" :key="g.mechanism" class="r9-group" :data-mechanism="g.mechanism">
            <h4>{{ g.mechanism }} <small>{{ g.rows.length }} 项</small></h4>
            <div class="data-table-wrap">
              <table class="data-table stack-table r9-points" data-testid="r9-points">
                <thead><tr><th>器件</th><th>品牌型号</th><th>计量方式</th><th>现场确认</th><th>编号</th><th>来源详情</th></tr></thead>
                <tbody>
                  <tr v-for="p in g.rows" :key="p.id" :data-point="p.pointCode">
                    <td data-label="器件"><b>{{ p.name }}</b><div class="cell-sub">{{ p.equipmentCode ? typeName(p.typeCode) : '作业统计（非设备）' }}</div></td>
                    <td data-label="品牌型号">{{ p.brandModel ?? '—' }}</td>
                    <td data-label="计量方式">{{ p.meters.map((m) => meterLabel(m)).join('、') || '—' }}</td>
                    <td data-label="现场确认"><span class="r9-tag" :class="p.confirmationStatus === 'CONFIRMED' ? 'ok' : 'warn'">{{ zh(CONFIRM_TEXT, p.confirmationStatus) }}</span><span v-if="p.openIssues" class="r9-tag over">{{ p.openIssues }} 项待核</span></td>
                    <td data-label="编号"><span class="cell-sub">{{ p.equipmentCode ?? '—' }}<br />点位 {{ p.pointCode }}</span></td>
                    <td data-label="来源详情">
                      <details class="r9-more">
                        <summary>查看</summary>
                        <dl>
                          <dt>PLC 地址（原文）</dt><dd><code>{{ p.plcAddressRaw ?? '—' }}</code> <span :class="{ warn: p.addressKind === 'UNPARSED' }">{{ zh(ADDRESS_KIND_TEXT, p.addressKind) }}</span></dd>
                          <dt>数据类型</dt><dd>{{ zh(RAW_DATA_TYPE_ZH, p.rawDataType) }} → {{ zh(DERIVED_TYPE_ZH, p.derivedType) }}</dd>
                          <dt>统计单位</dt><dd>原文「{{ p.unitRaw ?? '—' }}」 → {{ unitZh(p.unit) || '无单位' }}</dd>
                          <dt>采集来源</dt><dd>{{ zh(ACQUISITION_SOURCE_ZH, p.acquisitionSource) }}</dd>
                          <dt>寿命（原文）</dt><dd>{{ p.lifeText ?? '—' }}</dd>
                          <dt>原始记录</dt><dd>第 {{ p.sourceRow }} 行</dd>
                        </dl>
                      </details>
                    </td>
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
              <span class="r9-tag" :class="i.status === 'OPEN' ? 'over' : 'ok'">{{ zh(ISSUE_STATUS_ZH, i.status) }}</span>
              <b>{{ zh(ISSUE_ZH, i.issueCode, '其他核查事项') }}</b>
              <small>{{ i.pointCode ? (pointByCode.get(i.pointCode)?.name ?? '') + ' · 点位 ' + i.pointCode : '整机' }}{{ i.sourceRow ? ' · 第 ' + i.sourceRow + ' 行' : '' }}{{ issueAddress(i.detail) ? ' · ' + issueAddress(i.detail) : '' }}</small>
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
      <p v-if="hasSim" class="r9-banner sim" data-testid="r9-sim-banner">⚠ 以下含<b>模拟数据</b>（来自模拟 / 测试运行身份），仅用于验证计量与预警链路，不计入真实使用量、维护与合规记录。真实机械控制关闭。</p>
      <p v-if="!hasReal" class="r9-banner" data-testid="r9-no-real">真实硬件尚未接入（真实 PLC 未授权连接），真实计量为空。</p>
      <div class="r9-toolbar">
        <input v-model="meterSearch" type="search" placeholder="搜索器件、机构或点位编号" aria-label="搜索计量" />
        <select v-model="modeFilter" aria-label="数据来源"><option value="ALL">全部来源</option><option value="REAL">仅真实</option><option value="SIM">仅模拟 / 测试</option></select>
        <small class="r9-meta">共 {{ (meters.data.value ?? []).length }} 项计量 · 显示 {{ shownMeters.length }} 项 · 异常 {{ anomalyCount }} 项 · 每 15 秒刷新</small>
      </div>
      <p v-if="meters.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></p>
      <p v-else-if="meters.isError.value" class="workspace-error" role="alert">{{ errorText(meters.error.value) }}</p>
      <template v-else>
        <div v-for="g in meterGroups" :key="g.mechanism" class="r9-group" :data-meter-group="g.mechanism">
          <h4>{{ g.mechanism }} <small>{{ g.rows.length }} 项</small></h4>
          <div class="data-table-wrap">
            <table class="data-table stack-table" data-testid="r9-meters">
              <thead><tr><th>器件 / 指标</th><th>来源</th><th>累计 / 当日</th><th>器件使用量</th><th>最近采样 / 质量</th><th>计算说明</th></tr></thead>
              <tbody>
                <tr v-for="m in g.rows" :key="m.id + (m.sourceMode ?? '')" :data-meter="m.meterCode" :data-mode="m.sourceMode ?? 'NONE'" class="data-row" tabindex="0" @click="selectedMeter = m" @keyup.enter="selectedMeter = m">
                  <td data-label="器件 / 指标"><b>{{ meterName(m) }}</b> · {{ zh(METRIC_ZH, m.metric, '其他指标') }}<div class="cell-sub">点位 {{ m.pointCode }}</div></td>
                  <td data-label="来源"><span class="r9-tag" :class="sourceModeBadge(m.sourceMode).tone" :data-mode-badge="m.sourceMode ?? ''">{{ sourceModeBadge(m.sourceMode).text }}</span></td>
                  <td data-label="累计 / 当日">{{ m.kind === 'DAILY' ? formatNumber(m.today, m.unit) : formatNumber(m.accumulated, m.unit) }}<div v-if="m.status !== 'ACTIVE'" class="cell-sub warn">{{ zh(METER_STATUS_ZH, m.status) }}</div></td>
                  <td data-label="器件使用量">{{ m.equipmentUsage === null ? (m.kind === 'CUMULATIVE' && m.sourceMode ? '数据不足（无基准或初始使用量未知）' : '—') : formatNumber(m.equipmentUsage, m.unit) }}
                    <div v-if="m.trustState === 'DECREASE_PENDING'" class="cell-sub warn" data-testid="r9-meter-pending">计数低于可信累计（原始值 {{ formatNumber(m.pendingRawValue ?? null) }}），未计入，待恢复或管理员确认复位</div></td>
                  <td data-label="最近采样">{{ formatCloudTime(m.lastSampledAt) }}<div class="cell-sub">{{ m.lastQuality ? '质量' + zh(QUALITY_ZH, m.lastQuality) : '尚无读数' }}{{ m.trustState ? ' · ' + zh(TRUST_ZH, m.trustState) : '' }}</div></td>
                  <td data-label="计算说明"><small class="r9-method">{{ meterMethodZh(m, meterConfirmed(m)) }}</small></td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
        <p v-if="!shownMeters.length" class="workspace-empty">没有符合条件的计量。</p>
      </template>
      <section v-if="selectedMeter" class="r9-card" data-testid="r9-readings">
        <h3>{{ meterName(selectedMeter) }} · {{ zh(METRIC_ZH, selectedMeter.metric, '其他指标') }} · 最近 50 条读数 <button class="ghost-action" @click="selectedMeter = null">关闭</button></h3>
        <div class="data-table-wrap">
          <table class="data-table stack-table">
            <thead><tr><th>采样时间</th><th>来源</th><th>原始值</th><th>增量</th><th>累计</th><th>质量 / 异常</th></tr></thead>
            <tbody>
              <tr v-for="(r, i) in readings.data.value ?? []" :key="i">
                <td data-label="时间">{{ formatCloudTime(r.sampledAt) }}</td>
                <td data-label="来源"><span class="r9-tag" :class="sourceModeBadge(r.sourceMode).tone">{{ sourceModeBadge(r.sourceMode).text }}</span></td>
                <td data-label="原始值">{{ formatNumber(r.rawValue) }}</td>
                <td data-label="增量">{{ formatNumber(r.delta) }}</td>
                <td data-label="累计">{{ formatNumber(r.accumulated) }}</td>
                <td data-label="质量 / 异常">{{ zh(QUALITY_ZH, r.quality) }}{{ r.anomaly ? ' · ' + zh(ANOMALY_ZH, r.anomaly, '其他异常') : '' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </template>

    <!-- ============================================================ 寿命与维护 -->
    <template v-else-if="tab === 'maintenance'">
      <p v-if="notice" class="workspace-notice" role="status" data-testid="r9-notice">{{ notice }}</p>
      <p v-if="actionError" class="form-error" role="alert" data-testid="r9-error">{{ actionError }}</p>
      <p class="r9-banner">寿命与维护提醒只作为提示，不替代法定检验结论，系统不自动判定报废。原始清单中的寿命为<b>待审核初始规则</b>（{{ draftCount }} 条），审核批准并填写生效日期后才参与真实提醒。</p>
      <section class="r9-card" data-testid="r9-alerts">
        <h3>预警 <small>{{ openAlerts.length }} 条待处理</small></h3>
        <ul class="r9-issues r9-alert-list">
          <li v-for="a in alerts.data.value ?? []" :key="a.id" :data-stage="a.stage" :data-mode="a.sourceMode" :data-alert-status="a.status">
            <span class="r9-tag" :class="sourceModeBadge(a.sourceMode).tone">{{ sourceModeBadge(a.sourceMode).text }}</span>
            <b>{{ slotName(ruleOf(a)?.equipmentSlot) }}</b>
            <span class="r9-stage">{{ zh(STAGE_ZH, a.stage, '其他提醒') }}</span>
            <small>{{ a.ruleName }} · 第 {{ a.cycleNo }} 周期 · {{ formatCloudTime(a.triggeredAt) }}<template v-if="alertFacts(a)"> · {{ alertFacts(a) }}</template> · {{ zh(ALERT_STATUS_ZH, a.status) }}</small>
            <button v-if="canMaintain && a.status === 'OPEN'" class="ghost-action" :disabled="openWO.isPending.value" data-testid="r9-create-wo" @click="openWO.mutate(a.id)">生成工单</button>
          </li>
        </ul>
        <p v-if="!(alerts.data.value ?? []).length" class="workspace-empty">暂无预警。</p>
      </section>
      <div class="r9-toolbar">
        <select v-model="ruleFilter" aria-label="规则筛选"><option value="ACTIVE">已生效（含模拟测试规则）</option><option value="DRAFT">待审核</option><option value="ALL">全部</option></select>
      </div>
      <div class="data-table-wrap">
        <table class="data-table stack-table" data-testid="r9-rules">
          <thead><tr><th>规则</th><th>器件</th><th>类型 / 周期</th><th>状态</th><th>下次到期</th><th>依据</th></tr></thead>
          <tbody>
            <tr v-for="r in shownRules" :key="r.id + (r.sourceMode ?? '')" :data-rule="r.ruleCode" :data-mode="r.sourceMode ?? ''">
              <td data-label="规则"><b>{{ r.name }}</b><div class="cell-sub">{{ zh(RULE_KIND_ZH, r.kind) }} · 规则编号 {{ r.ruleCode }}</div>
                <span v-if="r.testOnly" class="r9-tag sim">模拟测试规则</span><span v-else class="r9-tag" :class="r.approvalStatus === 'APPROVED' ? 'ok' : 'warn'">{{ zh(APPROVAL_ZH, r.approvalStatus) }}</span></td>
              <td data-label="器件">{{ slotName(r.equipmentSlot) }}</td>
              <td data-label="类型 / 周期">{{ zh(RULE_TYPE_ZH, r.type) }}<div class="cell-sub">{{ r.intervalDays ? r.intervalDays + ' 天' : '' }}{{ r.intervalDays && r.intervalMeter ? ' / ' : '' }}{{ r.intervalMeter ? formatNumber(r.intervalMeter, usageOf(r.equipmentSlot, r.sourceMode)?.unit ?? '') : '' }}{{ !r.intervalDays && !r.intervalMeter ? '周期待定义' : '' }}</div></td>
              <td data-label="状态"><span class="r9-status" :data-tone="statusView(r.status ?? (r.approvalStatus === 'APPROVED' ? 'INSUFFICIENT_DATA' : 'DRAFT')).tone" :data-status="r.status ?? ''" :data-gaps="(r.dataGaps ?? []).join(',')">{{ ruleStatusText(r) }}</span>
                <div v-if="r.progressPercent !== null" class="cell-sub">使用量 {{ r.progressPercent }}%</div>
                <div v-if="r.reason" class="cell-sub warn" data-testid="r9-rule-reason">{{ r.reason }}</div></td>
              <td data-label="下次到期">{{ r.nextDueAt ? formatCloudTime(r.nextDueAt) : '—' }}<div class="cell-sub">{{ daysLeftText(r.daysLeft) }}{{ r.cycleNo ? ' · 第 ' + r.cycleNo + ' 周期' : '' }}</div></td>
              <td data-label="依据"><small>{{ r.testOnly ? '模拟 / 测试阈值规则，不是真实维护规则' : r.basis }}</small></td>
            </tr>
          </tbody>
        </table>
      </div>
      <section class="r9-card" data-testid="r9-orders">
        <h3>维护工单</h3>
        <div class="data-table-wrap">
          <table class="data-table stack-table">
            <thead><tr><th>工单</th><th>器件 / 类型</th><th>状态</th><th>责任人</th><th>创建 / 完成</th><th></th></tr></thead>
            <tbody>
              <tr v-for="w in orders.data.value ?? []" :key="w.id" :data-wo="w.code" :data-wo-status="w.status">
                <td data-label="工单"><b>{{ w.code }}</b> <span class="r9-tag" :class="sourceModeBadge(w.sourceMode).tone">{{ sourceModeBadge(w.sourceMode).text }}</span><div class="cell-sub">{{ w.title }}</div></td>
                <td data-label="器件 / 类型">{{ slotName(w.equipmentSlot) }}<div class="cell-sub">{{ zh(WO_TYPE_ZH, w.type) }}</div></td>
                <td data-label="状态">{{ zh(WO_STATUS_ZH, w.status) }}<div v-if="w.result" class="cell-sub">{{ zh(WO_RESULT_ZH, w.result) }}</div></td>
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
      <p v-else-if="history.isError.value" class="workspace-error" role="alert">{{ errorText(history.error.value) }}</p>
      <ol v-else class="r9-history" data-testid="r9-history">
        <li v-for="(h, i) in history.data.value ?? []" :key="i" :data-category="h.category" :data-mode="h.sourceMode">
          <span class="h-time">{{ formatCloudTime(h.at) }}</span>
          <span class="h-cat">{{ zh(HISTORY_CATEGORY_ZH, h.category, '其他记录') }} <span v-if="h.sourceMode !== 'REAL'" class="r9-tag sim">{{ sourceModeBadge(h.sourceMode).text }}</span></span>
          <span class="h-sum">{{ historyText(h.category, h.summary) }}</span>
          <span class="h-actor">{{ h.actor }}</span>
        </li>
      </ol>
      <p v-if="history.data.value && !history.data.value.length" class="workspace-empty">暂无历史记录。</p>
    </template>

    <!-- Work order dialog -->
    <div v-if="active" class="modal-backdrop" @click.self="!step.isPending.value && (active = null)">
      <form class="workspace-modal r9-wo" data-testid="r9-wo-dialog" novalidate @submit.prevent>
        <h2>{{ active.code }} <span class="r9-tag" :class="sourceModeBadge(active.sourceMode).tone">{{ sourceModeBadge(active.sourceMode).text }}</span></h2>
        <p class="form-hint">{{ active.title }} · {{ zh(WO_STATUS_ZH, active.status) }}<template v-if="active.equipmentSlot"> · {{ slotName(active.equipmentSlot) }}，当前使用量 {{ formatNumber(usageOf(active.equipmentSlot, active.sourceMode)?.equipmentUsage ?? null, usageOf(active.equipmentSlot, active.sourceMode)?.unit ?? '') }}</template></p>
        <template v-if="active.status === 'OPEN' || active.status === 'ASSIGNED'">
          <label>责任人<input v-model="form.assignee" data-testid="r9-wo-assignee" maxlength="80" /></label>
          <div class="modal-actions">
            <button type="button" class="ghost-action" :disabled="step.isPending.value || !form.assignee.trim()" data-testid="r9-wo-assign" @click="step.mutate('ASSIGNED')">分配</button>
            <button v-if="active.status === 'ASSIGNED'" type="button" class="primary-action" :disabled="step.isPending.value" data-testid="r9-wo-start" @click="step.mutate('IN_PROGRESS')">开始执行</button>
          </div>
        </template>
        <template v-if="active.status === 'IN_PROGRESS'">
          <label>结果<select v-model="form.result" data-testid="r9-wo-result"><option v-for="(t, k) in WO_RESULT_ZH" :key="k" :value="k">{{ t }}</option></select></label>
          <label>完成依据 / 工作内容 *<input v-model="form.basis" data-testid="r9-wo-basis" maxlength="200" /></label>
          <label class="check-row"><input v-model="form.replace" type="checkbox" data-testid="r9-wo-replace" />本次更换了器件（生成新的安装实例并重建计量基准，原始累计量不变）</label>
          <template v-if="form.replace">
            <label>新器件序列号<input v-model="form.serial" maxlength="120" /></label>
            <label>更换原因 *<input v-model="form.reason" data-testid="r9-wo-reason" maxlength="200" /></label>
          </template>
          <template v-if="form.rope">
            <fieldset class="r9-rope"><legend>钢丝绳检查（结论由检查人填写，系统不判定报废）</legend>
              <label>实测直径（毫米）<input v-model="form.diameter" inputmode="decimal" /></label>
              <label>可见断丝数<input v-model="form.broken" inputmode="numeric" /></label>
              <label>发现<input v-model="form.finding" maxlength="200" /></label>
              <label>检查结论 *<select v-model="form.conclusion" data-testid="r9-rope-conclusion"><option value="" disabled>请选择</option><option v-for="(t, k) in CONCLUSION_ZH" :key="k" :value="k">{{ t }}</option></select></label>
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
        <p v-if="resolveMut.error.value" class="form-error" role="alert">{{ errorZh(resolveMut.error.value) }}</p>
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
.r9-toolbar { display: flex; gap: 8px; flex-wrap: wrap; align-items: center; }
.r9-toolbar input[type='search'] { height: 32px; padding: 0 10px; border: 1px solid var(--line-soft); border-radius: 7px; background: var(--surface-900); color: var(--text-1); font-size: 13px; min-width: 0; flex: 1 1 220px; max-width: 360px; }
.r9-meta { color: var(--text-3); font-size: 12px; }
.r9-more summary { cursor: pointer; color: var(--accent, #5cc8ff); font-size: 12px; }
.r9-more dl { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: 2px 10px; margin: 6px 0 0; font-size: 12px; }
.r9-more dt { color: var(--text-3); }
.r9-more dd { margin: 0; color: var(--text-2); overflow-wrap: anywhere; }
.r9-more .warn { color: #e8b75d; }
.r9-method { color: var(--text-3); }
.r9-stage { color: #ff9f43; font-weight: 600; font-size: 13px; }
.r9-alert-list b { font-size: 14px; color: var(--text-1); }
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
