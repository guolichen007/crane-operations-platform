// R9 B6 — API client and pure presentation logic for first-asset points, metering, maintenance,
// work orders and the maintenance & due center. Every list carries sourceMode; SIMULATION / TEST
// values are always marked (sourceModeBadge) and never mixed into REAL counters.
import { api } from './client';
import {
  APPROVAL_ZH, CONCLUSION_ZH, GAP_ZH, HISTORY_CATEGORY_ZH, ISSUE_ZH, METRIC_ZH, PENDING_ATTR_ZH, RULE_KIND_ZH, RULE_TYPE_ZH, SCHEDULE_STATUS_ZH, STAGE_ZH,
  WO_STATUS_ZH, WO_TYPE_ZH, unitZh,
} from './zh';

export type SourceMode = 'REAL' | 'SIMULATION' | 'TEST';

export type PointView = {
  id: string; pointCode: string; name: string; mechanism: string; equipmentSlot: string | null; equipmentInstallationId: string | null;
  equipmentCode: string | null; typeCode: string | null; plcAddressRaw: string | null; addressKind: 'BIT' | 'WORD' | 'NONE' | 'UNPARSED';
  rawDataType: string; derivedType: string; unitRaw: string | null; unit: string; activeLevel: string | null; edge: string | null;
  acquisitionSource: string; confirmationStatus: string; quality: string; version: number; sourceRow: number; brandModel: string | null;
  lifeText: string | null; reviewFlags: string[]; openIssues: number; meters: string[];
};
export type IssueView = { id: string; issueCode: string; pointCode: string | null; sourceRow: number | null; detail: Record<string, unknown>; status: string; resolvedBy: string | null; resolution: string | null; configChangeId?: string | null };
export type PendingView = { id?: string; subject: string; attribute: string; candidate: unknown; basis: string; status: string; version?: number; confirmedValue?: unknown; decidedBy?: string | null; decidedAt?: string | null; decisionBasis?: string | null };
export type MeterView = {
  id: string; meterCode: string; pointCode: string; metric: string; kind: 'CUMULATIVE' | 'DAILY' | 'STATE'; unit: string; scope: string; equipmentSlot: string | null;
  formula: string; status: string; sourceMode: SourceMode | null; runtimeDeviceId: string | null; lastRawValue: number | null; accumulated: number | null;
  equipmentUsage: number | null; lastSampledAt: string | null; lastQuality: string | null; readings: number; today: number | null;
  trustState?: 'OK' | 'DECREASE_PENDING' | null; pendingRawValue?: number | null; pendingSince?: string | null; baselineKnown?: boolean;
};
export type ReadingView = { sampledAt: string; sourceMode: SourceMode; rawValue: number | null; quality: string; delta: number; accumulated: number; anomaly: string | null; bootId: string; sequence: number };
export type RuleView = {
  id: string; ruleCode: string; version: number; name: string; kind: string; type: 'TIME_BASED' | 'METER_BASED' | 'HYBRID'; equipmentSlot: string | null; meterCode: string | null;
  intervalDays: number | null; intervalMeter: number | null; leadDays: number[]; warnPercents: number[]; basis: string; approvalStatus: string; approvedBy: string | null;
  effectiveFrom: string | null; testOnly: boolean; sourceMode: SourceMode | null; cycleNo: number | null; status: string | null; nextDueAt: string | null;
  nextDueMeter: number | null; progressPercent: number | null; daysLeft: number | null; evaluatedAt: string | null;
  dataGaps?: ('TIME' | 'METER')[]; reason?: string | null; sourceRow?: number | null; approvedAt?: string | null;
};
export type AlertView = { id: string; ruleId: string; ruleName: string; assetId: string; sourceMode: SourceMode; cycleNo: number; stage: string; triggeredAt: string; status: string; workOrderId: string | null; detail: Record<string, unknown>;
  equipmentSlot?: string | null; equipmentName?: string | null; mechanism?: string | null };
export type WorkOrder = {
  id: string; code: string; projectId: string; assetId: string; sourceMode: SourceMode; equipmentInstallationId: string | null; equipmentSlot: string | null;
  ruleId: string | null; alertId: string | null; cycleNo: number | null; type: string; priority: string; status: 'OPEN' | 'ASSIGNED' | 'IN_PROGRESS' | 'COMPLETED' | 'CANCELLED';
  title: string; description: string; createdBy: string; createdAt: string; assignedTo: string | null; plannedAt: string | null; dueAt: string | null;
  startedAt: string | null; completedAt: string | null; completedBy: string | null; result: string | null; basis: string | null; notes: string; version: number;
};
export type HistoryEntry = { at: string; category: string; sourceMode: SourceMode; actor: string; summary: string; reference: string };
export type CenterRow = {
  projectId: string; projectName: string; assetId: string; assetName: string; assetCode: string | null; mechanism: string | null; equipmentSlot: string | null;
  equipmentName?: string | null; equipmentType: string | null; category: string; ruleId: string | null; ruleName: string; approvalStatus: string; sourceMode: SourceMode; status: string;
  nextDueAt: string | null; progressPercent: number | null; daysLeft: number | null; openWorkOrders: number;
};
export type AttentionCounts = {
  maintenanceDue: number; maintenanceDueSoon: number; inspectionDue: number; inspectionUnknown: number; openWorkOrders: number;
  insufficientData: number; draftRules: number; simulationDue: number; simulationWorkOrders: number;
};
export type Center = { rows: CenterRow[]; alerts: AlertView[]; workOrders: WorkOrder[]; attention: AttentionCounts; generatedAt: string };

const a = (id: string) => `assets/${encodeURIComponent(id)}`;
export const listPoints = (id: string) => api<PointView[]>(`${a(id)}/points`);
export const listIssues = (id: string) => api<IssueView[]>(`${a(id)}/point-issues`);
export const listPending = (id: string) => api<PendingView[]>(`${a(id)}/pending-attributes`);
export const listMeters = (id: string) => api<MeterView[]>(`${a(id)}/meters`);
export const listReadings = (id: string, meter: string) => api<ReadingView[]>(`${a(id)}/meters/${encodeURIComponent(meter)}/readings`, { query: { limit: 50 } });
export const listRules = (id: string) => api<RuleView[]>(`${a(id)}/maintenance-rules`);
export const listAlerts = (id: string) => api<AlertView[]>(`${a(id)}/maintenance-alerts`);
export const listWorkOrders = (id: string) => api<WorkOrder[]>(`${a(id)}/work-orders`);
export const listHistory = (id: string) => api<HistoryEntry[]>(`${a(id)}/maintenance-history`);
export const getCenter = () => api<Center>('maintenance/center');
export const createWorkOrder = (alertId: string) => api<WorkOrder>(`maintenance-alerts/${encodeURIComponent(alertId)}/work-order`, { method: 'POST' });
export type Transition = {
  to: WorkOrder['status']; assignedTo?: string; result?: 'DONE' | 'DONE_WITH_FINDINGS' | 'NOT_DONE'; basis?: string; note?: string;
  replacement?: { serialNumber: string; catalogItemId: string; reason: string; initialUsage: number | null };
  ropeInspection?: { inspectionType: string; measuredDiameterMm: number | null; brokenWiresVisible: number | null; wear: string; corrosion: string; deformation: string; lubrication: string; finding: string; conclusion: string; nextDueAt: string | null };
};
export const transitionWorkOrder = (id: string, version: number, t: Transition) =>
  api<WorkOrder>(`work-orders/${encodeURIComponent(id)}/transitions`, { method: 'POST', body: t, ifMatch: version });
export const resolveIssue = (assetId: string, issueId: string, status: 'RESOLVED' | 'ACCEPTED', resolution: string) =>
  api<void>(`${a(assetId)}/point-issues/${encodeURIComponent(issueId)}/resolve`, { method: 'POST', body: { status, resolution } });

// ---------------------------------------------------------------------------------------------
// R9 收口 — real-asset readiness (platform_admin writes; If-Match on every decision)
// ---------------------------------------------------------------------------------------------
export type ReadinessSlot = { equipmentSlot: string; equipmentInstallationId: string; equipmentCode: string; typeCode: string; installedAt: string | null; version: number };
export type ReadinessBaseline = { equipmentSlot: string; meterCode: string; sourceMode: SourceMode; initialUsage: number | null; baselineKind: string; recordedBy: string; recordedAt: string };
export type Readiness = {
  assetVersion: number; ratedCapacityT: number | null; spanM: number | null; useRegistrationNo: string | null; latestInspectionDate: string | null; inspectionValidUntil: string | null;
  pointsTotal: number; pointsConfirmed: number; issuesOpen: number; pendingOpen: number; slotsTotal: number; slotsWithInstalledAt: number;
  rulesApproved: number; rulesDraft: number; realRuntimeRegistered: boolean; realBaselines: number; slots: ReadinessSlot[]; baselines: ReadinessBaseline[]; blockers: string[];
};
export type PointChange = { id: string; pointCode: string; fromVersion: number; toVersion: number; before: Record<string, unknown>; after: Record<string, unknown>; basis: string; actor: string; changedAt: string; resolvedIssues: string[] | null };
export const getReadiness = (id: string) => api<Readiness>(`${a(id)}/readiness`);
export const listPointChanges = (id: string) => api<PointChange[]>(`${a(id)}/point-changes`);
export const decidePending = (id: string, pendingId: string, version: number, body: { status: 'CONFIRMED' | 'REJECTED'; value: unknown; basis: string }) =>
  api<void>(`${a(id)}/pending-attributes/${encodeURIComponent(pendingId)}/decision`, { method: 'POST', body, ifMatch: version });
export const setInstalledAt = (id: string, slot: string, version: number, installedAt: string, basis: string) =>
  api<void>(`${a(id)}/equipment-slots/${encodeURIComponent(slot)}/installed-at`, { method: 'POST', body: { installedAt, basis }, ifMatch: version });
export type PointConfirmation = { status: 'CONFIRMED' | 'REJECTED'; plcAddressRaw?: string | null; activeLevel?: 'HIGH' | 'LOW'; edge?: 'RISING' | 'FALLING' | 'BOTH';
  rawDataType?: 'INT' | 'FLOAT' | 'STRING' | 'BOOL'; unit?: 'count' | 'h' | 't' | 'none'; basis: string; resolveIssues: string[] };
export const confirmPoint = (id: string, point: string, version: number, body: PointConfirmation) =>
  api<void>(`${a(id)}/points/${encodeURIComponent(point)}/confirmation`, { method: 'POST', body, ifMatch: version });
export const recordBaseline = (id: string, body: { equipmentSlot: string; meterCode: string; initialUsage: number; note: string }) =>
  api<void>(`${a(id)}/meter-baselines`, { method: 'POST', body });
export const approveRule = (id: string, rule: string, version: number, body: { intervalDays: number | null; intervalMeter: number | null; effectiveFrom: string; basis: string }) =>
  api<void>(`${a(id)}/maintenance-rules/${encodeURIComponent(rule)}/approve`, { method: 'POST', body, ifMatch: version });
export const setStatutoryInspection = (id: string, version: number, body: { useRegistrationNo: string | null; latestInspectionDate: string; inspectionValidUntil: string; basis: string }) =>
  api<void>(`${a(id)}/statutory-inspection`, { method: 'POST', body, ifMatch: version });
export const confirmCounterReset = (id: string, meter: string, body: { sourceMode: SourceMode; reason: string }) =>
  api<void>(`${a(id)}/meters/${encodeURIComponent(meter)}/counter-reset`, { method: 'POST', body });

export const GAP_TEXT: Record<string, string> = { TIME: '日期条件数据不足', METER: '计量条件数据不足' };
export { GAP_ZH };
/** Rule status line: a partial (HYBRID one-condition) result is shown as such, never as plain 正常. */
export function ruleStatusText(r: Pick<RuleView, 'status' | 'approvalStatus' | 'dataGaps' | 'type'>): string {
  const base = statusView(r.status ?? (r.approvalStatus === 'APPROVED' ? 'INSUFFICIENT_DATA' : 'DRAFT')).text;
  const gaps = r.dataGaps ?? [];
  if (!r.status || !gaps.length) return base;
  if (r.type === 'HYBRID' && gaps.length === 1) return r.status === 'INSUFFICIENT_DATA' ? '部分数据不足' : `${base}（部分数据不足）`;
  return r.status === 'INSUFFICIENT_DATA' ? base : `${base}（${gaps.map((g) => GAP_TEXT[g] ?? g).join('、')}）`;
}

// ---------------------------------------------------------------------------------------------
// Presentation
// ---------------------------------------------------------------------------------------------

export const MODE_TEXT: Record<SourceMode, string> = { REAL: '真实', SIMULATION: '模拟', TEST: '测试' };
export function sourceModeBadge(mode: SourceMode | null | undefined): { text: string; tone: 'real' | 'sim' | 'none' } {
  if (!mode) return { text: '无数据', tone: 'none' };
  return mode === 'REAL' ? { text: '真实', tone: 'real' } : { text: mode === 'TEST' ? '测试数据' : '模拟数据', tone: 'sim' };
}

// R9-UX: every map below is the shared Chinese dictionary of cloud/zh.ts (single source); the names
// stay for the existing imports. statusView never falls back to the raw enum.
export const STATUS_TEXT: Record<string, { text: string; tone: 'ok' | 'warn' | 'due' | 'over' | 'none' }> = {
  NORMAL: { text: SCHEDULE_STATUS_ZH.NORMAL, tone: 'ok' },
  DUE_SOON: { text: SCHEDULE_STATUS_ZH.DUE_SOON, tone: 'warn' },
  DUE: { text: SCHEDULE_STATUS_ZH.DUE, tone: 'due' },
  OVERDUE: { text: SCHEDULE_STATUS_ZH.OVERDUE, tone: 'over' },
  INSUFFICIENT_DATA: { text: SCHEDULE_STATUS_ZH.INSUFFICIENT_DATA, tone: 'none' },
  DRAFT: { text: SCHEDULE_STATUS_ZH.DRAFT, tone: 'none' },
};
export const statusView = (s: string | null | undefined) => (s ? STATUS_TEXT[s] ?? { text: '状态未知', tone: 'none' as const } : { text: '—', tone: 'none' as const });

export const STAGE_TEXT = STAGE_ZH;
export const RULE_TYPE_TEXT = RULE_TYPE_ZH;
export const KIND_TEXT = RULE_KIND_ZH;
export const APPROVAL_TEXT = APPROVAL_ZH;
export const WO_STATUS_TEXT = WO_STATUS_ZH;
export const WO_TYPE_TEXT = WO_TYPE_ZH;
export const CATEGORY_TEXT: Record<string, string> = {
  MAINTENANCE_DUE: '维护到期', CALIBRATION_DUE: '校准到期', INSPECTION_DUE: '检查到期', REPLACEMENT_DUE: '寿命 / 更换到期', STATUTORY_INSPECTION: '法定检验',
};
export const METRIC_TEXT = METRIC_ZH;
export const ADDRESS_KIND_TEXT: Record<string, string> = { BIT: '开关量位地址', WORD: '字地址', NONE: '无 PLC 地址', UNPARSED: '地址待核（无法解析）' };
export const CONFIRM_TEXT: Record<string, string> = { UNCONFIRMED: '待现场确认', CONFIRMED: '已现场确认', REJECTED: '已否决' };
export const ISSUE_TEXT = ISSUE_ZH;
export const PENDING_TEXT = PENDING_ATTR_ZH;
export const HISTORY_TEXT = HISTORY_CATEGORY_ZH;
export const ROPE_CONCLUSION_TEXT = CONCLUSION_ZH;

export function formatNumber(v: number | null | undefined, unit = ''): string {
  if (v === null || v === undefined) return '—';
  const n = Math.abs(v) >= 1000 ? v.toLocaleString('zh-CN', { maximumFractionDigits: 0 }) : v.toLocaleString('zh-CN', { maximumFractionDigits: 3 });
  const u = unitZh(unit);
  return u ? `${n} ${u}` : n;
}

/** Days-left phrase; null means the due date cannot be computed (数据不足). */
export function daysLeftText(days: number | null | undefined): string {
  if (days === null || days === undefined) return '—';
  if (days < 0) return `逾期 ${-days} 天`;
  if (days === 0) return '今日到期';
  return `剩余 ${days} 天`;
}

/** Group points by mechanism (source order kept) for the ledger tab. */
export function groupByMechanism<T extends { mechanism: string }>(rows: T[]): { mechanism: string; rows: T[] }[] {
  const out: { mechanism: string; rows: T[] }[] = [];
  for (const r of rows) {
    const g = out.find((x) => x.mechanism === r.mechanism);
    if (g) g.rows.push(r);
    else out.push({ mechanism: r.mechanism, rows: [r] });
  }
  return out;
}

/** Center filters (client side; the server already scopes to authorized projects). */
export type CenterFilter = { project: string; asset: string; mechanism: string; slot: string; status: string; mode: 'REAL' | 'SIM' | 'ALL' };
export function filterCenter(rows: CenterRow[], f: CenterFilter): CenterRow[] {
  return rows.filter((r) =>
    (!f.project || r.projectId === f.project) && (!f.asset || r.assetId === f.asset) && (!f.mechanism || r.mechanism === f.mechanism) &&
    (!f.slot || r.equipmentSlot === f.slot) && (!f.status || r.status === f.status) &&
    (f.mode === 'ALL' || (f.mode === 'REAL' ? r.sourceMode === 'REAL' : r.sourceMode !== 'REAL')));
}
