<script setup lang="ts">
// R9 收口 G3 — 实车接入准备: every step that turns an imported, unverified fact into a trusted basis
// for REAL maintenance is an explicit platform_admin decision with basis, optimistic version, history
// and audit (server-enforced; this page only offers the entries). Viewers see the same state read-only.
import { computed, ref } from 'vue';
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query';
import {
  getReadiness, listPending, listPoints, listIssues, listRules, listPointChanges, listMeters,
  decidePending, setInstalledAt, confirmPoint, recordBaseline, approveRule, setStatutoryInspection, confirmCounterReset,
  PENDING_TEXT, ISSUE_TEXT, CONFIRM_TEXT, ADDRESS_KIND_TEXT, APPROVAL_TEXT, RULE_TYPE_TEXT, formatNumber, sourceModeBadge,
  type PointView, type PendingView, type RuleView, type MeterView,
} from '../cloud/maintenance';
import { formatCloudTime as fmt } from '../cloud/format';
import { ApiError } from '../cloud/client';

const props = defineProps<{ assetId: string; canAdmin: boolean }>();
const formatCloudTime = (v: string | null | undefined) => fmt(v ?? undefined);
const queryClient = useQueryClient();
const id = computed(() => props.assetId);
const readiness = useQuery({ queryKey: computed(() => ['r9-readiness', id.value]), queryFn: () => getReadiness(id.value) });
const pending = useQuery({ queryKey: computed(() => ['r9-pending', id.value]), queryFn: () => listPending(id.value) });
const points = useQuery({ queryKey: computed(() => ['r9-points', id.value]), queryFn: () => listPoints(id.value) });
const issues = useQuery({ queryKey: computed(() => ['r9-issues', id.value]), queryFn: () => listIssues(id.value) });
const rules = useQuery({ queryKey: computed(() => ['r9-rules', id.value]), queryFn: () => listRules(id.value) });
const changes = useQuery({ queryKey: computed(() => ['r9-point-changes', id.value]), queryFn: () => listPointChanges(id.value) });
const meters = useQuery({ queryKey: computed(() => ['r9-meters', id.value]), queryFn: () => listMeters(id.value) });

const notice = ref('');
const actionError = ref('');
const errorText = (e: unknown) =>
  e instanceof ApiError ? (e.status === 409 ? `数据已被他人修改或状态不允许：${e.message}（已刷新，请核对后重试）` : e.status === 403 ? '无权限（仅平台管理员）' : e.message) : '网络错误，请稍后重试';
const refresh = () => Promise.all(['r9-readiness', 'r9-pending', 'r9-points', 'r9-issues', 'r9-rules', 'r9-point-changes', 'r9-meters', 'r9-center'].map((k) => queryClient.invalidateQueries({ queryKey: [k] })));
const done = async (text: string) => {
  notice.value = text;
  actionError.value = '';
  dialog.value = null;
  await refresh();
};
const fail = async (e: unknown) => {
  actionError.value = errorText(e);
  if (e instanceof ApiError && e.status === 409) await refresh();
};

type Dialog =
  | { kind: 'pending'; p: PendingView; status: 'CONFIRMED' | 'REJECTED'; value: string; basis: string }
  | { kind: 'installed'; slot: string; version: number; date: string; basis: string }
  | { kind: 'point'; p: PointView; status: 'CONFIRMED' | 'REJECTED'; address: string; activeLevel: string; edge: string; rawDataType: string; unit: string; basis: string; issues: string[] }
  | { kind: 'baseline'; slot: string; meterCode: string; initialUsage: string; note: string }
  | { kind: 'rule'; r: RuleView; days: string; meter: string; effectiveFrom: string; basis: string }
  | { kind: 'inspection'; reg: string; latest: string; valid: string; basis: string }
  | { kind: 'reset'; m: MeterView; reason: string };
const dialog = ref<Dialog | null>(null);
const today = () => new Date().toISOString().slice(0, 10);

const openIssuesOf = (code: string) => (issues.data.value ?? []).filter((i) => i.pointCode === code && i.status === 'OPEN');
const pointFilter = ref<'UNCONFIRMED' | 'CONFIRMED' | 'ALL'>('UNCONFIRMED');
const shownPoints = computed(() => (points.data.value ?? []).filter((p) => pointFilter.value === 'ALL' || (pointFilter.value === 'CONFIRMED' ? p.confirmationStatus !== 'UNCONFIRMED' : p.confirmationStatus === 'UNCONFIRMED')));
const draftRules = computed(() => (rules.data.value ?? []).filter((r) => !r.testOnly && r.approvalStatus === 'DRAFT_UNAPPROVED' && !r.sourceMode));
const approvedRules = computed(() => (rules.data.value ?? []).filter((r) => !r.testOnly && r.approvalStatus === 'APPROVED'));
const pendingMeters = computed(() => (meters.data.value ?? []).filter((m) => m.trustState === 'DECREASE_PENDING'));
const cumulativeMeters = computed(() => {
  const seen = new Set<string>();
  return (meters.data.value ?? []).filter((m) => m.kind === 'CUMULATIVE' && m.equipmentSlot && !seen.has(m.meterCode) && seen.add(m.meterCode));
});

const save = useMutation({
  mutationFn: async () => {
    const d = dialog.value!;
    switch (d.kind) {
      case 'pending': {
        let value: unknown = null;
        if (d.status === 'CONFIRMED') {
          const n = Number(d.value);
          value = d.value.trim() !== '' && Number.isFinite(n) ? n : d.value.trim();
        }
        await decidePending(id.value, d.p.id!, d.p.version!, { status: d.status, value, basis: d.basis.trim() });
        return `已${d.status === 'CONFIRMED' ? '确认' : '否决'}：${PENDING_TEXT[d.p.attribute] ?? d.p.attribute}`;
      }
      case 'installed':
        await setInstalledAt(id.value, d.slot, d.version, new Date(d.date + 'T00:00:00+08:00').toISOString(), d.basis.trim());
        return `已登记 ${d.slot} 实际安装日期`;
      case 'point':
        await confirmPoint(id.value, d.p.pointCode, d.p.version, {
          status: d.status, plcAddressRaw: d.address.trim() || null, basis: d.basis.trim(), resolveIssues: d.issues,
          ...(d.activeLevel ? { activeLevel: d.activeLevel as 'HIGH' | 'LOW' } : {}), ...(d.edge ? { edge: d.edge as 'RISING' } : {}),
          ...(d.rawDataType ? { rawDataType: d.rawDataType as 'INT' } : {}), ...(d.unit ? { unit: d.unit as 'count' } : {}),
        });
        return `点位 ${d.p.pointCode} 已${d.status === 'CONFIRMED' ? '确认' : '否决'}`;
      case 'baseline':
        await recordBaseline(id.value, { equipmentSlot: d.slot, meterCode: d.meterCode, initialUsage: Number(d.initialUsage), note: d.note.trim() });
        return `已登记 ${d.meterCode} 真实初始计量基准`;
      case 'rule':
        await approveRule(id.value, d.r.id, d.r.version, {
          intervalDays: d.days.trim() ? Number(d.days) : null, intervalMeter: d.meter.trim() ? Number(d.meter) : null,
          effectiveFrom: new Date(d.effectiveFrom + 'T00:00:00+08:00').toISOString(), basis: d.basis.trim(),
        });
        return `规则 ${d.r.ruleCode} 已批准生效`;
      case 'inspection':
        await setStatutoryInspection(id.value, readiness.data.value!.assetVersion, { useRegistrationNo: d.reg.trim() || null, latestInspectionDate: d.latest, inspectionValidUntil: d.valid, basis: d.basis.trim() });
        return '已登记法定检验';
      case 'reset':
        await confirmCounterReset(id.value, d.m.id, { sourceMode: d.m.sourceMode!, reason: d.reason.trim() });
        return `已确认 ${d.m.meterCode} 计数复位`;
    }
  },
  onSuccess: (text) => done(text),
  onError: fail,
});
const valid = computed(() => {
  const d = dialog.value;
  if (!d) return false;
  const b = (s: string) => s.trim().length >= 2;
  switch (d.kind) {
    case 'pending': return b(d.basis) && (d.status === 'REJECTED' || d.value.trim() !== '');
    case 'installed': return b(d.basis) && !!d.date && d.date <= today();
    case 'point': return b(d.basis) && (d.status === 'REJECTED' || d.issues.length === openIssuesOf(d.p.pointCode).length);
    case 'baseline': return b(d.note) && d.initialUsage.trim() !== '' && Number(d.initialUsage) >= 0;
    case 'rule': return b(d.basis) && !!d.effectiveFrom && (!!d.days.trim() || !!d.meter.trim() || !!d.r.intervalDays || !!d.r.intervalMeter);
    case 'inspection': return b(d.basis) && !!d.latest && !!d.valid && d.valid > d.latest;
    case 'reset': return b(d.reason);
  }
  return false;
});
const startPoint = (p: PointView, status: 'CONFIRMED' | 'REJECTED') => {
  dialog.value = { kind: 'point', p, status, address: p.plcAddressRaw ?? '', activeLevel: p.activeLevel ?? '', edge: p.edge ?? '', rawDataType: '', unit: '', basis: '', issues: openIssuesOf(p.pointCode).map((i) => i.id) };
  actionError.value = '';
};
const issueNames = (codes: string[]) => codes.map((c) => ISSUE_TEXT[c] ?? c).join('、');
const valueText = (v: unknown) => (v === null || v === undefined ? '—' : typeof v === 'object' ? JSON.stringify(v) : String(v));
</script>
<template>
  <section class="r9-panel" data-testid="r9-readiness">
    <p class="r9-banner" data-testid="r9-ready-banner">实车接入准备：以下每一项都需由平台管理员依据现场资料确认，并留存依据、版本与审计。<b>未经确认的点位只能用于模拟验收，不能作为真实寿命 / 维护计量</b>；53 条原始寿命规则草案保持未批准，逐条审批后才生效。真实硬件尚未接入。</p>
    <p v-if="notice" class="workspace-notice" role="status" data-testid="r9-ready-notice">{{ notice }}</p>
    <p v-if="actionError" class="form-error" role="alert" data-testid="r9-ready-error">{{ actionError }}</p>
    <p v-if="readiness.isLoading.value" class="skeleton table-skeleton" aria-busy="true"></p>
    <template v-else-if="readiness.data.value">
      <section class="r9-card" data-testid="r9-ready-summary">
        <h3>接入准备状态 <small>{{ readiness.data.value.blockers.length ? '尚不能作为真实维护依据' : '准备项已完成' }}</small></h3>
        <div class="r9-kpis">
          <span :data-kpi="'points'">点位确认 <b>{{ readiness.data.value.pointsConfirmed }} / {{ readiness.data.value.pointsTotal }}</b></span>
          <span :data-kpi="'issues'">核查事项待处理 <b>{{ readiness.data.value.issuesOpen }}</b></span>
          <span :data-kpi="'pending'">待确认属性 <b>{{ readiness.data.value.pendingOpen }}</b></span>
          <span :data-kpi="'slots'">安装日期 <b>{{ readiness.data.value.slotsWithInstalledAt }} / {{ readiness.data.value.slotsTotal }}</b></span>
          <span :data-kpi="'baselines'">真实计量基准 <b>{{ readiness.data.value.realBaselines }}</b></span>
          <span :data-kpi="'rules'">规则 已批准 <b>{{ readiness.data.value.rulesApproved }}</b> · 草案 <b>{{ readiness.data.value.rulesDraft }}</b></span>
          <span :data-kpi="'inspection'">法定检验 <b>{{ readiness.data.value.inspectionValidUntil ? '有效期至 ' + readiness.data.value.inspectionValidUntil : '未登记' }}</b></span>
          <span :data-kpi="'runtime'">真实运行身份 <b>{{ readiness.data.value.realRuntimeRegistered ? '已登记' : '未登记' }}</b></span>
        </div>
        <ul class="r9-issues" data-testid="r9-ready-blockers">
          <li v-for="b in readiness.data.value.blockers" :key="b"><span class="r9-tag warn">待完成</span>{{ b }}</li>
        </ul>
      </section>

      <section class="r9-card" data-testid="r9-ready-nameplate">
        <h3>铭牌参数、实际位置与其他待确认属性</h3>
        <ul class="r9-pending">
          <li v-for="p in pending.data.value ?? []" :key="p.id ?? p.attribute" :data-attr="p.attribute" :data-status="p.status">
            <b>{{ p.subject === 'asset' ? '整机' : p.subject }} · {{ PENDING_TEXT[p.attribute] ?? p.attribute }}</b>
            <span class="r9-tag" :class="p.status === 'PENDING' ? 'warn' : p.status === 'CONFIRMED' ? 'ok' : 'over'">{{ p.status === 'PENDING' ? '待确认' : p.status === 'CONFIRMED' ? '已确认 ' + valueText(p.confirmedValue) : '已否决' }}</span>
            <small>候选 {{ valueText(p.candidate) }} · {{ p.basis }}</small>
            <small v-if="p.decidedBy">{{ p.decidedBy }} · {{ formatCloudTime(p.decidedAt) }} · 依据：{{ p.decisionBasis }}</small>
            <template v-if="canAdmin && p.status === 'PENDING' && p.attribute !== 'statutoryInspection'">
              <button class="ghost-action" data-testid="r9-pending-confirm" @click="dialog = { kind: 'pending', p, status: 'CONFIRMED', value: p.candidate === null || p.candidate === undefined ? '' : String(p.candidate), basis: '' }; actionError = ''">确认</button>
              <button class="ghost-action" @click="dialog = { kind: 'pending', p, status: 'REJECTED', value: '', basis: '' }; actionError = ''">否决</button>
            </template>
          </li>
        </ul>
      </section>

      <section class="r9-card" data-testid="r9-ready-inspection">
        <h3>法定检验
          <button v-if="canAdmin" class="ghost-action" data-testid="r9-inspection-open" @click="dialog = { kind: 'inspection', reg: readiness.data.value.useRegistrationNo ?? '', latest: '', valid: '', basis: '' }; actionError = ''">登记 / 更新</button>
        </h3>
        <p class="form-hint">使用登记号 {{ readiness.data.value.useRegistrationNo ?? '未登记' }} · 最近检验 {{ readiness.data.value.latestInspectionDate ?? '—' }} · 有效期至 {{ readiness.data.value.inspectionValidUntil ?? '—' }}</p>
      </section>

      <section class="r9-card" data-testid="r9-ready-slots">
        <h3>器件实际安装日期 <small>以竣工 / 更换记录为准，不使用导入时间</small></h3>
        <div class="data-table-wrap">
          <table class="data-table stack-table">
            <thead><tr><th>器件</th><th>类型</th><th>实际安装日期</th><th></th></tr></thead>
            <tbody>
              <tr v-for="s in readiness.data.value.slots" :key="s.equipmentSlot" :data-slot="s.equipmentSlot">
                <td data-label="器件"><b>{{ s.equipmentSlot }}</b><div class="cell-sub">{{ s.equipmentCode }}</div></td>
                <td data-label="类型">{{ s.typeCode }}</td>
                <td data-label="安装日期">{{ s.installedAt ? formatCloudTime(s.installedAt) : '未登记' }}</td>
                <td data-label="操作"><button v-if="canAdmin" class="ghost-action" data-testid="r9-installed-open" @click="dialog = { kind: 'installed', slot: s.equipmentSlot, version: s.version, date: '', basis: '' }; actionError = ''">登记</button></td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section class="r9-card" data-testid="r9-ready-points">
        <h3>点位现场确认（地址 / 有效电平 / 边沿 / 数据类型 / 单位）
          <select v-model="pointFilter" aria-label="点位筛选"><option value="UNCONFIRMED">待确认</option><option value="CONFIRMED">已确认 / 已否决</option><option value="ALL">全部</option></select>
        </h3>
        <div class="data-table-wrap">
          <table class="data-table stack-table">
            <thead><tr><th>点位</th><th>地址</th><th>电平 / 边沿</th><th>类型 / 单位</th><th>核查事项</th><th></th></tr></thead>
            <tbody>
              <tr v-for="p in shownPoints" :key="p.id" :data-ready-point="p.pointCode">
                <td data-label="点位"><b>{{ p.pointCode }}</b> {{ p.name }}<div class="cell-sub">{{ CONFIRM_TEXT[p.confirmationStatus] ?? p.confirmationStatus }} · v{{ p.version }}</div></td>
                <td data-label="地址"><code>{{ p.plcAddressRaw ?? '—' }}</code><div class="cell-sub">{{ ADDRESS_KIND_TEXT[p.addressKind] }}</div></td>
                <td data-label="电平 / 边沿">{{ p.activeLevel ?? '未确认' }} / {{ p.edge ?? '未确认' }}</td>
                <td data-label="类型 / 单位">{{ p.rawDataType }} / {{ p.unit }}</td>
                <td data-label="核查">{{ issueNames(openIssuesOf(p.pointCode).map((iss) => iss.issueCode)) || '—' }}</td>
                <td data-label="操作"><template v-if="canAdmin && p.confirmationStatus === 'UNCONFIRMED'">
                  <button class="ghost-action" data-testid="r9-point-confirm" @click="startPoint(p, 'CONFIRMED')">确认</button>
                  <button class="ghost-action" @click="startPoint(p, 'REJECTED')">否决</button></template></td>
              </tr>
            </tbody>
          </table>
        </div>
        <details v-if="(changes.data.value ?? []).length" class="r9-changes">
          <summary>点位配置变更记录（{{ changes.data.value!.length }}）</summary>
          <ul class="r9-issues">
            <li v-for="c in changes.data.value" :key="c.id"><b>{{ c.pointCode }}</b> v{{ c.fromVersion }}→v{{ c.toVersion }} <small>{{ c.actor }} · {{ formatCloudTime(c.changedAt) }} · {{ c.basis }}</small>
              <small v-if="c.resolvedIssues?.length">处理核查：{{ issueNames(c.resolvedIssues) }}</small></li>
          </ul>
        </details>
      </section>

      <section class="r9-card" data-testid="r9-ready-baselines">
        <h3>初始计量基准 <small>真实基准需填写现场读数依据；未知则不登记（相应规则显示数据不足，不当作 0）</small></h3>
        <ul class="r9-issues">
          <li v-for="b in readiness.data.value.baselines" :key="b.meterCode + b.sourceMode"><span class="r9-tag" :class="sourceModeBadge(b.sourceMode).tone">{{ sourceModeBadge(b.sourceMode).text }}</span>
            <b>{{ b.equipmentSlot }} · {{ b.meterCode }}</b> <small>初始使用量 {{ b.initialUsage === null ? '未知' : formatNumber(b.initialUsage) }} · {{ b.recordedBy }} · {{ formatCloudTime(b.recordedAt) }}</small></li>
        </ul>
        <button v-if="canAdmin" class="ghost-action" data-testid="r9-baseline-open" @click="dialog = { kind: 'baseline', slot: cumulativeMeters[0]?.equipmentSlot ?? '', meterCode: cumulativeMeters[0]?.meterCode ?? '', initialUsage: '', note: '' }; actionError = ''">登记真实初始基准</button>
      </section>

      <section v-if="pendingMeters.length" class="r9-card" data-testid="r9-ready-trust">
        <h3>计数异常待确认 <small>计数低于可信累计：恢复后自动解除；确属 PLC 计数复位需管理员确认</small></h3>
        <ul class="r9-issues">
          <li v-for="m in pendingMeters" :key="m.id + m.sourceMode"><span class="r9-tag" :class="sourceModeBadge(m.sourceMode).tone">{{ sourceModeBadge(m.sourceMode).text }}</span>
            <b>{{ m.meterCode }}</b> <small>可信累计 {{ formatNumber(m.accumulated) }} · 当前原始值 {{ formatNumber(m.pendingRawValue ?? null) }} · 自 {{ formatCloudTime(m.pendingSince ?? null) }}</small>
            <button v-if="canAdmin" class="ghost-action" @click="dialog = { kind: 'reset', m, reason: '' }; actionError = ''">确认计数复位</button></li>
        </ul>
      </section>

      <section class="r9-card" data-testid="r9-ready-rules">
        <h3>维护规则审批 <small>草案 {{ draftRules.length }} 条（未生效）· 已批准 {{ approvedRules.length }} 条</small></h3>
        <div class="data-table-wrap">
          <table class="data-table stack-table">
            <thead><tr><th>规则</th><th>类型 / 周期</th><th>依据（原文）</th><th></th></tr></thead>
            <tbody>
              <tr v-for="r in [...approvedRules, ...draftRules]" :key="r.id" :data-ready-rule="r.ruleCode">
                <td data-label="规则"><b>{{ r.name }}</b><div class="cell-sub">{{ r.ruleCode }} · v{{ r.version }} · <span class="r9-tag" :class="r.approvalStatus === 'APPROVED' ? 'ok' : 'warn'">{{ APPROVAL_TEXT[r.approvalStatus] }}</span></div></td>
                <td data-label="类型 / 周期">{{ RULE_TYPE_TEXT[r.type] }}<div class="cell-sub">{{ r.intervalDays ? r.intervalDays + ' 天' : '' }}{{ r.intervalMeter ? ' ' + formatNumber(r.intervalMeter) : '' }}{{ !r.intervalDays && !r.intervalMeter ? '待定义' : '' }}</div></td>
                <td data-label="依据"><small>{{ r.basis }}</small><div v-if="r.approvedBy" class="cell-sub">批准：{{ r.approvedBy }} · 生效 {{ formatCloudTime(r.effectiveFrom) }}</div></td>
                <td data-label="操作"><button v-if="canAdmin && r.approvalStatus === 'DRAFT_UNAPPROVED'" class="ghost-action" data-testid="r9-rule-approve-open" @click="dialog = { kind: 'rule', r, days: r.intervalDays ? String(r.intervalDays) : '', meter: r.intervalMeter ? String(r.intervalMeter) : '', effectiveFrom: today(), basis: '' }; actionError = ''">审批</button></td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </template>
    <p v-else-if="readiness.isError.value" class="workspace-error" role="alert">{{ errorText(readiness.error.value) }}</p>

    <div v-if="dialog" class="modal-backdrop" @click.self="!save.isPending.value && (dialog = null)">
      <form class="workspace-modal r9-wo" data-testid="r9-ready-dialog" novalidate @submit.prevent="valid && save.mutate()">
        <template v-if="dialog.kind === 'pending'">
          <h2>{{ dialog.status === 'CONFIRMED' ? '确认' : '否决' }}：{{ PENDING_TEXT[dialog.p.attribute] ?? dialog.p.attribute }}</h2>
          <p class="form-hint">{{ dialog.p.basis }}</p>
          <label v-if="dialog.status === 'CONFIRMED'">确认值 *<input v-model="dialog.value" data-testid="r9-ready-value" maxlength="200" /></label>
        </template>
        <template v-else-if="dialog.kind === 'installed'">
          <h2>{{ dialog.slot }} 实际安装日期</h2>
          <label>安装日期 *<input v-model="dialog.date" type="date" :max="today()" data-testid="r9-ready-date" /></label>
        </template>
        <template v-else-if="dialog.kind === 'point'">
          <h2>{{ dialog.status === 'CONFIRMED' ? '确认' : '否决' }}点位 {{ dialog.p.pointCode }} {{ dialog.p.name }}</h2>
          <template v-if="dialog.status === 'CONFIRMED'">
            <label>现场核实地址（DBn.DBXb.bit 或 DBn.DBWb）<input v-model="dialog.address" data-testid="r9-ready-address" maxlength="40" /></label>
            <label>有效电平<select v-model="dialog.activeLevel" data-testid="r9-ready-level"><option value="">未填写</option><option value="HIGH">高电平有效</option><option value="LOW">低电平有效</option></select></label>
            <label>计数边沿<select v-model="dialog.edge" data-testid="r9-ready-edge"><option value="">未填写</option><option value="RISING">上升沿</option><option value="FALLING">下降沿</option><option value="BOTH">双边沿</option></select></label>
            <label>数据类型（不改留空）<select v-model="dialog.rawDataType"><option value="">{{ dialog.p.rawDataType }}</option><option>INT</option><option>FLOAT</option><option>BOOL</option><option>STRING</option></select></label>
            <label>单位（不改留空）<select v-model="dialog.unit"><option value="">{{ dialog.p.unit }}</option><option value="count">次</option><option value="h">小时</option><option value="t">吨</option><option value="none">无</option></select></label>
          </template>
          <fieldset v-if="openIssuesOf(dialog.p.pointCode).length" class="r9-rope"><legend>随本次变更处理的核查事项（确认时须全部处理）</legend>
            <label v-for="i in openIssuesOf(dialog.p.pointCode)" :key="i.id" class="check-row"><input v-model="dialog.issues" type="checkbox" :value="i.id" />{{ ISSUE_TEXT[i.issueCode] ?? i.issueCode }}</label>
          </fieldset>
        </template>
        <template v-else-if="dialog.kind === 'baseline'">
          <h2>登记真实初始计量基准</h2>
          <label>计量<select v-model="dialog.meterCode" data-testid="r9-ready-meter" @change="dialog.slot = cumulativeMeters.find((m) => m.meterCode === (dialog as { meterCode: string }).meterCode)?.equipmentSlot ?? ''">
            <option v-for="m in cumulativeMeters" :key="m.meterCode" :value="m.meterCode">{{ m.equipmentSlot }} · {{ m.meterCode }}</option></select></label>
          <label>计量开始前已使用量（新件填 0）*<input v-model="dialog.initialUsage" inputmode="decimal" data-testid="r9-ready-initial" /></label>
        </template>
        <template v-else-if="dialog.kind === 'rule'">
          <h2>审批 {{ dialog.r.ruleCode }}（v{{ dialog.r.version }}）</h2>
          <p class="form-hint">{{ dialog.r.name }} · {{ dialog.r.basis }}</p>
          <label v-if="dialog.r.type !== 'METER_BASED'">周期（天）<input v-model="dialog.days" inputmode="numeric" data-testid="r9-ready-days" /></label>
          <label v-if="dialog.r.type !== 'TIME_BASED'">计量周期<input v-model="dialog.meter" inputmode="decimal" data-testid="r9-ready-interval" /></label>
          <label>生效日期 *<input v-model="dialog.effectiveFrom" type="date" data-testid="r9-ready-effective" /></label>
        </template>
        <template v-else-if="dialog.kind === 'inspection'">
          <h2>登记法定检验</h2>
          <label>使用登记号<input v-model="dialog.reg" maxlength="80" data-testid="r9-ready-reg" /></label>
          <label>最近检验日期 *<input v-model="dialog.latest" type="date" :max="today()" data-testid="r9-ready-latest" /></label>
          <label>有效期至 *<input v-model="dialog.valid" type="date" data-testid="r9-ready-valid" /></label>
        </template>
        <template v-else-if="dialog.kind === 'reset'">
          <h2>确认 {{ dialog.m.meterCode }} 计数复位</h2>
          <p class="form-hint">仅在确认 PLC 计数确实清零 / 更换模块时操作。确认后：可信累计 {{ formatNumber(dialog.m.accumulated) }} + 复位后读数 {{ formatNumber(dialog.m.pendingRawValue ?? null) }}。进程重启不是复位。</p>
        </template>
        <label v-if="dialog.kind === 'baseline'">依据（现场读数记录）*<input v-model="dialog.note" maxlength="300" data-testid="r9-ready-basis" /></label>
        <label v-else-if="dialog.kind === 'reset'">原因 *<input v-model="dialog.reason" maxlength="200" data-testid="r9-ready-basis" /></label>
        <label v-else>依据（铭牌照片 / 图纸 / 现场记录编号）*<input v-model="dialog.basis" maxlength="300" data-testid="r9-ready-basis" /></label>
        <p v-if="actionError" class="form-error" role="alert">{{ actionError }}</p>
        <div class="modal-actions">
          <button type="button" class="ghost-action" :disabled="save.isPending.value" @click="dialog = null">取消</button>
          <button type="submit" class="primary-action" :disabled="save.isPending.value || !valid" data-testid="r9-ready-save">保存</button>
        </div>
      </form>
    </div>
  </section>
</template>
<style scoped>
.r9-panel { display: flex; flex-direction: column; gap: 12px; }
.r9-card { border: 1px solid var(--line-soft); border-radius: 12px; padding: 12px 14px; display: flex; flex-direction: column; gap: 8px; min-width: 0; max-width: 100%; box-sizing: border-box; }
.r9-card h3 { margin: 0; font-size: 14px; color: var(--text-1); display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.r9-card h3 small { color: var(--text-3); font-weight: 400; }
.r9-kpis { display: flex; flex-wrap: wrap; gap: 6px 16px; font-size: 13px; color: var(--text-2); }
.r9-kpis b { color: var(--text-1); }
.r9-tag { display: inline-block; font-size: 11px; line-height: 18px; padding: 0 6px; border-radius: 9px; border: 1px solid currentColor; margin-right: 4px; white-space: nowrap; }
.r9-tag.sim { color: #ff9f43; background: rgba(255, 159, 67, 0.12); font-weight: 600; }
.r9-tag.real, .r9-tag.ok { color: #55d6a4; }
.r9-tag.warn { color: #e8b75d; }
.r9-tag.over { color: #f08a8a; }
.r9-tag.none { color: var(--text-3); }
.r9-banner { margin: 0; padding: 8px 12px; border-radius: 8px; border: 1px solid var(--line-soft); color: var(--text-2); font-size: 13px; }
.r9-pending, .r9-issues { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
.r9-pending li, .r9-issues li { display: flex; flex-wrap: wrap; gap: 4px 8px; align-items: baseline; font-size: 13px; color: var(--text-2); border-bottom: 1px dashed var(--line-soft); padding-bottom: 6px; }
.r9-pending small, .r9-issues small { color: var(--text-3); overflow-wrap: anywhere; }
.r9-card select, .r9-wo input, .r9-wo select { height: 32px; padding: 0 8px; border: 1px solid var(--line-soft); border-radius: 7px; background: var(--surface-900); color: var(--text-1); font-size: 13px; }
.r9-changes summary { cursor: pointer; font-size: 12px; color: var(--text-3); }
code { font-size: 12px; overflow-wrap: anywhere; }
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
  .r9-wo { width: 100vw; border-radius: 0; }
}
</style>
