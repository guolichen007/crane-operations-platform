// R9 B6 — API client and pure presentation logic for first-asset points, metering, maintenance,
// work orders and the maintenance & due center. Every list carries sourceMode; SIMULATION / TEST
// values are always marked (sourceModeBadge) and never mixed into REAL counters.
import { api } from './client';

export type SourceMode = 'REAL' | 'SIMULATION' | 'TEST';

export type PointView = {
  id: string; pointCode: string; name: string; mechanism: string; equipmentSlot: string | null; equipmentInstallationId: string | null;
  equipmentCode: string | null; typeCode: string | null; plcAddressRaw: string | null; addressKind: 'BIT' | 'WORD' | 'NONE' | 'UNPARSED';
  rawDataType: string; derivedType: string; unitRaw: string | null; unit: string; activeLevel: string | null; edge: string | null;
  acquisitionSource: string; confirmationStatus: string; quality: string; version: number; sourceRow: number; brandModel: string | null;
  lifeText: string | null; reviewFlags: string[]; openIssues: number; meters: string[];
};
export type IssueView = { id: string; issueCode: string; pointCode: string | null; sourceRow: number | null; detail: Record<string, unknown>; status: string; resolvedBy: string | null; resolution: string | null };
export type PendingView = { subject: string; attribute: string; candidate: unknown; basis: string; status: string };
export type MeterView = {
  id: string; meterCode: string; pointCode: string; metric: string; kind: 'CUMULATIVE' | 'DAILY' | 'STATE'; unit: string; scope: string; equipmentSlot: string | null;
  formula: string; status: string; sourceMode: SourceMode | null; runtimeDeviceId: string | null; lastRawValue: number | null; accumulated: number | null;
  equipmentUsage: number | null; lastSampledAt: string | null; lastQuality: string | null; readings: number; today: number | null;
};
export type ReadingView = { sampledAt: string; sourceMode: SourceMode; rawValue: number | null; quality: string; delta: number; accumulated: number; anomaly: string | null; bootId: string; sequence: number };
export type RuleView = {
  id: string; ruleCode: string; version: number; name: string; kind: string; type: 'TIME_BASED' | 'METER_BASED' | 'HYBRID'; equipmentSlot: string | null; meterCode: string | null;
  intervalDays: number | null; intervalMeter: number | null; leadDays: number[]; warnPercents: number[]; basis: string; approvalStatus: string; approvedBy: string | null;
  effectiveFrom: string | null; testOnly: boolean; sourceMode: SourceMode | null; cycleNo: number | null; status: string | null; nextDueAt: string | null;
  nextDueMeter: number | null; progressPercent: number | null; daysLeft: number | null; evaluatedAt: string | null;
};
export type AlertView = { id: string; ruleId: string; ruleName: string; assetId: string; sourceMode: SourceMode; cycleNo: number; stage: string; triggeredAt: string; status: string; workOrderId: string | null; detail: Record<string, unknown> };
export type WorkOrder = {
  id: string; code: string; projectId: string; assetId: string; sourceMode: SourceMode; equipmentInstallationId: string | null; equipmentSlot: string | null;
  ruleId: string | null; alertId: string | null; cycleNo: number | null; type: string; priority: string; status: 'OPEN' | 'ASSIGNED' | 'IN_PROGRESS' | 'COMPLETED' | 'CANCELLED';
  title: string; description: string; createdBy: string; createdAt: string; assignedTo: string | null; plannedAt: string | null; dueAt: string | null;
  startedAt: string | null; completedAt: string | null; completedBy: string | null; result: string | null; basis: string | null; notes: string; version: number;
};
export type HistoryEntry = { at: string; category: string; sourceMode: SourceMode; actor: string; summary: string; reference: string };
export type CenterRow = {
  projectId: string; projectName: string; assetId: string; assetName: string; assetCode: string | null; mechanism: string | null; equipmentSlot: string | null;
  equipmentType: string | null; category: string; ruleId: string | null; ruleName: string; approvalStatus: string; sourceMode: SourceMode; status: string;
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
// Presentation
// ---------------------------------------------------------------------------------------------

export const MODE_TEXT: Record<SourceMode, string> = { REAL: '真实', SIMULATION: '模拟', TEST: '测试' };
export function sourceModeBadge(mode: SourceMode | null | undefined): { text: string; tone: 'real' | 'sim' | 'none' } {
  if (!mode) return { text: '无数据', tone: 'none' };
  return mode === 'REAL' ? { text: '真实', tone: 'real' } : { text: mode === 'TEST' ? '测试数据' : '模拟数据', tone: 'sim' };
}

export const STATUS_TEXT: Record<string, { text: string; tone: 'ok' | 'warn' | 'due' | 'over' | 'none' }> = {
  NORMAL: { text: '正常', tone: 'ok' },
  DUE_SOON: { text: '即将到期', tone: 'warn' },
  DUE: { text: '到期', tone: 'due' },
  OVERDUE: { text: '逾期', tone: 'over' },
  INSUFFICIENT_DATA: { text: '数据不足', tone: 'none' },
  DRAFT: { text: '待审核（未生效）', tone: 'none' },
};
export const statusView = (s: string | null | undefined) => STATUS_TEXT[s ?? ''] ?? { text: s ?? '—', tone: 'none' as const };

export const STAGE_TEXT: Record<string, string> = {
  LEAD_90: '提前 90 天', LEAD_30: '提前 30 天', LEAD_7: '提前 7 天', DUE: '到期', OVERDUE: '逾期', PCT_80: '使用量 80%', PCT_90: '使用量 90%', PCT_100: '使用量 100%',
};
export const RULE_TYPE_TEXT: Record<string, string> = { TIME_BASED: '按时间', METER_BASED: '按计量', HYBRID: '时间/计量先到者' };
export const KIND_TEXT: Record<string, string> = { MAINTENANCE: '保养', CALIBRATION: '校准', INSPECTION: '检查', REPLACEMENT: '寿命/更换' };
export const APPROVAL_TEXT: Record<string, string> = { DRAFT_UNAPPROVED: '待审核（未生效）', APPROVED: '已批准', RETIRED: '已停用' };
export const WO_STATUS_TEXT: Record<string, string> = { OPEN: '待分配', ASSIGNED: '已分配', IN_PROGRESS: '执行中', COMPLETED: '已完成', CANCELLED: '已取消' };
export const WO_TYPE_TEXT: Record<string, string> = { PREVENTIVE: '预防性维护', CALIBRATION: '校准', INSPECTION: '检查', CORRECTIVE: '纠正性维修', REPLACEMENT: '更换' };
export const CATEGORY_TEXT: Record<string, string> = {
  MAINTENANCE_DUE: '维护到期', CALIBRATION_DUE: '校准到期', INSPECTION_DUE: '检查到期', REPLACEMENT_DUE: '寿命/更换到期', STATUTORY_INSPECTION: '法定检验',
};
export const METRIC_TEXT: Record<string, string> = {
  cycles: '动作次数', hours: '运行小时', mechanism_running_hours: '机构运行小时', daily_auto_hours: '当日自动作业时长', production_tonnes: '当日作业吨数',
  daily_job_count: '当日作业次数', state: '状态',
};
export const ADDRESS_KIND_TEXT: Record<string, string> = { BIT: 'DBX 位', WORD: '字', NONE: '无地址', UNPARSED: '地址待核（无法解析）' };
export const CONFIRM_TEXT: Record<string, string> = { UNCONFIRMED: '待现场确认', CONFIRMED: '已确认', REJECTED: '已否决' };
export const ISSUE_TEXT: Record<string, string> = {
  DUPLICATE_PLC_ADDRESS_GROUP: '重复 PLC 地址组', MISSING_BIT_INDEX: '地址缺少位索引', THERMAL_RELAY_HOURS_VS_COUNT: '热继统计口径（次数/小时）不一致',
  UNIT_CONFLICT_HOURS_VS_TONNES: '吨数单位写为「小时」', CHECK_RAW_BIT_VS_PUBLISHED_METRIC_TYPE: 'DBX 位信号与 INT/FLOAT 发布值需核对',
  ONE_SIGNAL_MULTIPLE_DERIVED_METERS: '一个信号派生多个指标', SHARED_HOIST_BRAKE_SOURCE: '钢丝绳使用量借用起升抱闸信号（代理量）',
  ROPE_COUNT_AND_INSTALLATION_UNKNOWN: '钢丝绳根数与安装未知', INSPECTION_REQUIRED_NO_AUTO_DISCARD: '需人工检查，系统不判定报废',
  DUPLICATE_ADDRESS_DIFFERENT_EQUIPMENT: '同一地址对应不同设备', DUPLICATE_ADDRESS_MAY_BE_SHARED_STATUS: '同一地址可能是共用状态', ADDRESS_SHARED_REVIEW_REQUIRED: '共用地址需复核',
  NON_PLC_SOURCE_UNKNOWN: '非 PLC 来源未知', LIDAR_2D_OR_3D_UNCONFIRMED: '激光雷达 2D/3D 未确认', SOURCE_NOT_SPECIFIED: '数据来源未注明',
  ASSET_INSTALLATION_DATES_AND_BASELINES_UNKNOWN: '安装日期与初始累计值未知', MODEL_INTERPRETATION_PENDING_NAMEPLATE: '型号解读待铭牌确认',
};
export const PENDING_TEXT: Record<string, string> = {
  ratedCapacityT: '额定起重量（t）', spanM: '跨度（m）', location: '现场位置', statutoryInspection: '使用登记与法定检验', ropeCount: '钢丝绳根数',
  lidarDimension: '激光雷达维度', rfidEntity: 'RFID 实体类型', radarType: '雷达类型',
};
export const HISTORY_TEXT: Record<string, string> = { LEDGER: '台账', REPLACEMENT: '更换', WORK_ORDER: '工单', WIRE_ROPE: '钢丝绳检查', FAULT: '故障' };
export const ROPE_CONCLUSION_TEXT: Record<string, string> = {
  CONTINUE: '继续使用', CONTINUE_MONITOR: '继续使用并加强监测', REPLACE_RECOMMENDED: '建议更换（由责任人决定）', STOP_PENDING_REVIEW: '停用待复核',
};

export function formatNumber(v: number | null | undefined, unit = ''): string {
  if (v === null || v === undefined) return '—';
  const n = Math.abs(v) >= 1000 ? v.toLocaleString('zh-CN', { maximumFractionDigits: 0 }) : v.toLocaleString('zh-CN', { maximumFractionDigits: 3 });
  return unit && unit !== 'none' ? `${n} ${unit === 'count' ? '次' : unit}` : n;
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
