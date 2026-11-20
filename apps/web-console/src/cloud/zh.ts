// R9-UX 全平台中文化 — the single shared Chinese presentation module.
//
// Rules (enforced by tests/zh.test.ts and the premium visible-text counterexamples):
//  * Every business enum shown to a user goes through a dictionary here. An unknown value is shown
//    as 「未知（…）」-style Chinese text — never the bare program enum. The raw code is kept for the
//    technical detail line (`techCode`) and for data-* attributes, never as the primary text.
//  * Stable values (enum codes, API values, MQTT, data-testid / data-*) are never changed here.
//  * Equipment numbers, asset / point / work-order codes, PLC addresses, brand / model, standard
//    numbers, user free text and the original Excel text are data, not UI copy: shown as-is.
import { ApiError, NetworkWriteError } from './client';

export const PRODUCT_NAME = '友道智造 · 行车综合管理平台';

/** Look a code up; unknown → 「<unknownLabel>」 (the raw code only as secondary technical detail). */
export function zh(dict: Record<string, string>, code: string | null | undefined, unknownLabel = '未知'): string {
  if (code === null || code === undefined || code === '') return '—';
  return dict[code] ?? unknownLabel;
}
/** Secondary technical line for a code that is shown translated (empty when the code is absent). */
export const techCode = (code: string | null | undefined): string => (code ? `编码 ${code}` : '');

// ---------------------------------------------------------------------------------------------
// Equipment types — the 51 code-owned types (internal/equipment/types.go, same Chinese names).
// The live dictionary from GET /equipment/types wins when loaded; this copy keeps pages that do not
// load it (台账、监测、接入准备) Chinese without an extra request.
// ---------------------------------------------------------------------------------------------
export const EQUIPMENT_TYPE_TEXT: Record<string, string> = {
  MOTOR: '电机', REDUCER: '减速机', BRAKE: '制动器', DRUM: '卷筒', HOOK: '吊钩', GRAB: '抓斗', WHEEL: '车轮', COUPLING: '联轴器',
  PLC: 'PLC 控制器', PLC_IO: 'PLC I/O 模块', SAFETY_PLC: '安全 PLC', VFD: '变频器', SERVO_DRIVE: '伺服驱动器', RELAY: '继电器', CONTACTOR: '接触器', REMOTE_CONTROLLER: '遥控器',
  LIDAR_3D: '三维激光雷达', LIDAR_2D: '二维激光雷达', CAMERA_RGB: '彩色相机', CAMERA_DEPTH: '深度相机', CAMERA_STEREO: '双目相机', CAMERA_THERMAL: '热成像相机',
  ENCODER: '编码器', ROPE_ENCODER: '拉绳编码器', LOAD_CELL: '称重传感器', IMU: '惯性测量单元', LIMIT_SWITCH: '限位开关', PROXIMITY_SENSOR: '接近传感器',
  ANTI_COLLISION_SENSOR: '防撞传感器', WIND_SENSOR: '风速传感器', TEMPERATURE_SENSOR: '温度传感器', VIBRATION_SENSOR: '振动传感器',
  INDUSTRIAL_PC: '工控机', EDGE_GATEWAY: '边缘网关', GPU: 'GPU 计算单元', INDUSTRIAL_SWITCH: '工业交换机', POE_SWITCH: 'PoE 交换机', ROUTER: '路由器', MEDIA_CONVERTER: '光电转换器',
  POWER_SUPPLY: '电源', UPS: '不间断电源', TRANSFORMER: '变压器', CONDUCTOR_RAIL: '滑触线',
  THERMAL_OVERLOAD_RELAY: '热过载继电器', MOTOR_PROTECTOR: '电机保护器', SOLENOID_VALVE: '电磁阀', RFID_READER: 'RFID 读写器', RADAR: '雷达（微波 / 毫米波）',
  LIDAR: '激光雷达（维度待确认）', BARRIER_GATE: '道闸', WIRE_ROPE: '钢丝绳',
};
export const EQUIPMENT_DOMAIN_TEXT: Record<string, string> = {
  MECHANICAL: '机械', CONTROL: '控制', PERCEPTION: '感知', COMPUTE: '计算', NETWORK: '网络', POWER: '电源', SENSOR: '传感', OTHER: '其他',
};
export const equipmentTypeName = (code: string | null | undefined, live?: readonly { typeCode: string; displayName: string }[]): string =>
  !code ? '—' : live?.find((t) => t.typeCode === code)?.displayName ?? EQUIPMENT_TYPE_TEXT[code] ?? '未登记的设备类型';

// ---------------------------------------------------------------------------------------------
// Roles, statuses
// ---------------------------------------------------------------------------------------------
export const PROJECT_ROLE_ZH: Record<string, string> = { viewer: '只读', operator: '操作员', maintainer: '维护员', platform_admin: '项目管理员' };
export const PORTFOLIO_ROLE_ZH: Record<string, string> = { portfolio_planner: '项目组合规划者', portfolio_manager: '项目组合经理' };
export const SYSTEM_ROLE_ZH: Record<string, string> = { system_admin: '系统管理员', account_manager: '账号管理员' };
/** Whole-machine asset types (internal/asset AssetTypes). */
export const ASSET_TYPE_ZH: Record<string, string> = { CRANE: '起重机', GANTRY: '门式起重机', BRIDGE: '桥式起重机', PORTAL: '门座起重机', JIB: '悬臂起重机' };
export const ASSET_STATUS_ZH: Record<string, string> = { DRAFT: '草稿', ACTIVE: '在用', OUT_OF_SERVICE: '停用', RETIRED: '已退役' };
export const LOCATION_KIND_ZH: Record<string, string> = { SITE: '场地', PLANT: '厂区', WORKSHOP: '车间', BAY: '工位', RUNWAY: '轨道', OTHER: '其他' };
export const LOCATION_STATUS_ZH: Record<string, string> = { ACTIVE: '启用', DISABLED: '已停用' };
export const COORDINATE_SYSTEM_ZH: Record<string, string> = { WGS84: 'WGS84 坐标', GCJ02: 'GCJ02 坐标', LOCAL_SITE: '现场局部坐标', UNKNOWN: '坐标未知' };
export const CATALOG_STATUS_ZH: Record<string, string> = { ACTIVE: '启用', DISABLED: '已停用' };
export const CRITICALITY_ZH: Record<string, string> = { LOW: '低', MEDIUM: '中', HIGH: '高', SAFETY_CRITICAL: '安全关键' };
export const ADMIN_STATE_ZH: Record<string, string> = { IN_SERVICE: '在用', MAINTENANCE: '维护中', OUT_OF_SERVICE: '停用', FAULT_REPORTED: '已报故障' };
export const PROJECT_LIFECYCLE_ZH: Record<string, string> = { PLANNED: '规划中', PRE_DEVELOPMENT: '前期开发', COMPLETED: '已完成', OPERATIONAL: '运营中' };
export const RELATION_TYPE_ZH: Record<string, string> = {
  CONTAINS: '包含', MOUNTED_ON: '安装于', DRIVES: '驱动', CONTROLS: '控制', SENSES: '感知', MONITORS: '监测', POWERED_BY: '供电来自', NETWORKED_TO: '网络连接', PROTECTS: '保护', BACKS_UP: '备份',
};

// Runtime identity / connection / acquisition
export const SOURCE_MODE_ZH: Record<string, string> = { REAL: '真实', SIMULATION: '模拟', TEST: '测试' };
export const CONNECTIVITY_ZH: Record<string, string> = { MQTT_CONNECTED: '在线', OFFLINE: '离线', UNKNOWN: '连接未知' };
export const OPERATING_STATE_ZH: Record<string, string> = { SILENT: '静默监测', LIVE: '实时采集', EVENT: '事件上报', DISCONNECTED: '采集断开', UNKNOWN: '采集状态未知' };
export const UPSTREAM_STATUS_ZH: Record<string, string> = { SILENT: '未开启实时', STARTING: '正在开启实时', LIVE: '实时采集中', STOPPING: '正在停止实时', FAILED: '实时开启失败', EXPIRED: '实时已过期' };
export const HEALTH_ZH: Record<string, string> = { EDGE_HEALTHY: '正常', CONTROLLER_HEALTHY: '正常', HEALTHY: '正常', DEGRADED: '降级', UNKNOWN: '未知' };
export const DISCONNECT_REASON_ZH: Record<string, string> = { NONE: '—', GRACEFUL: '正常下线', UNEXPECTED: '异常断开', MIGRATED: '连接已迁移' };
export const RUN_MODE_ZH: Record<string, string> = { AUTO: '自动', MANUAL: '手动', UNKNOWN: '未知' };

// Points, meters
export const RAW_DATA_TYPE_ZH: Record<string, string> = { INT: '整数', FLOAT: '浮点数', STRING: '文本', BOOL: '开关量' };
/** Select options: Chinese name with the necessary technical abbreviation kept in brackets. */
export const RAW_DATA_TYPE_OPTION: Record<string, string> = { INT: '整数（INT）', FLOAT: '浮点数（FLOAT）', BOOL: '开关量（BOOL）', STRING: '文本（STRING）' };
export const DERIVED_TYPE_ZH: Record<string, string> = { EDGE_COUNT: '动作计次', ON_DURATION: '通电时长累计', DAILY_VALUE: '当日统计值', EVENT_LIST: '事件列表', STATE: '状态量' };
export const ACQUISITION_SOURCE_ZH: Record<string, string> = { PLC: 'PLC 采集', LIDAR_DRIVER: '激光雷达驱动', EDGE_APP: '边缘应用', UNKNOWN: '来源待确认' };
export const UNIT_ZH: Record<string, string> = { count: '次', h: '小时', t: '吨', none: '' };
export const unitZh = (u: string | null | undefined): string => (u === null || u === undefined ? '' : UNIT_ZH[u] ?? u);
export const ACTIVE_LEVEL_ZH: Record<string, string> = { HIGH: '高电平有效', LOW: '低电平有效' };
export const EDGE_ZH: Record<string, string> = { RISING: '上升沿', FALLING: '下降沿', BOTH: '双边沿' };
export const QUALITY_ZH: Record<string, string> = { GOOD: '正常', UNCERTAIN: '可疑', BAD: '异常', MISSING: '缺失' };
export const POINT_QUALITY_ZH: Record<string, string> = { UNVERIFIED: '未核实', VERIFIED: '已核实', SUSPECT: '存疑' };
export const METER_KIND_ZH: Record<string, string> = { CUMULATIVE: '累计', DAILY: '当日', STATE: '状态' };
export const METER_SCOPE_ZH: Record<string, string> = { EQUIPMENT: '器件', MECHANISM: '机构', ASSET: '整机' };
export const METER_STATUS_ZH: Record<string, string> = { PENDING_REVIEW: '口径待审核', ACTIVE: '已确认启用', UNIT_CONFLICT_PENDING: '单位冲突待确认', DISABLED: '已停用' };
export const TRUST_ZH: Record<string, string> = { OK: '可信', DECREASE_PENDING: '计数下降待确认' };
export const ANOMALY_ZH: Record<string, string> = {
  COUNTER_RESET: '疑似计数复位（未计入）', DECREASE: '计数下降（未计入）', GAP: '数据缺失', OUT_OF_ORDER: '迟到数据（仅存档）', QUALITY: '质量异常（未计入）', FIRST: '首次读数（建立基准）',
};
export const METRIC_ZH: Record<string, string> = {
  cycles: '动作次数', hours: '运行时长', mechanism_running_hours: '机构运行时长', daily_auto_hours: '当日自动作业时长', production_tonnes: '当日作业吨数',
  daily_job_count: '当日作业次数', state: '状态',
};

/**
 * Chinese calculation note of a meter (replaces the English formula text). `confirmed` = the point
 * was confirmed on site (CONFIRMED): only then is the counting rule stated as fact; otherwise it is
 * shown as the candidate method pending site confirmation.
 */
export function meterMethodZh(m: { metric: string; kind: string; formula?: string | null }, confirmed: boolean): string {
  const proxy = (m.formula ?? '').startsWith('PROXY');
  const conflict = (m.formula ?? '').includes('unit conflict');
  let text: string;
  switch (m.metric) {
    case 'cycles': text = '对 PLC 开关量信号的有效边沿计次，由边缘端累计，云端保存累计值'; break;
    case 'hours': text = proxy ? '代理量：借用起升抱闸接触器的通电时长，仅作钢丝绳使用量参考，不作为报废依据' : '累计开关量信号的通电时长，由边缘端累计'; break;
    case 'mechanism_running_hours': text = '用同一抱闸接触器信号的通电时长折算机构运行时长（原始清单备注）'; break;
    case 'daily_auto_hours': text = '边缘端上报的当日自动作业时长，按日清零'; break;
    case 'production_tonnes': text = conflict ? '边缘端上报的当日作业吨数；原始清单「统计」列写为「小时」，单位待确认' : '边缘端上报的当日作业吨数，按日清零'; break;
    case 'daily_job_count': text = '边缘端上报的当日作业次数，按日清零'; break;
    default: text = '状态量，不累计';
  }
  if (m.kind === 'CUMULATIVE' && !confirmed) return `候选口径（待现场确认）：${text}`;
  return text;
}

// Maintenance
export const RULE_KIND_ZH: Record<string, string> = { MAINTENANCE: '保养', CALIBRATION: '校准', INSPECTION: '检查', REPLACEMENT: '寿命 / 更换' };
export const RULE_TYPE_ZH: Record<string, string> = { TIME_BASED: '按时间', METER_BASED: '按计量', HYBRID: '时间与计量先到者' };
export const APPROVAL_ZH: Record<string, string> = { DRAFT_UNAPPROVED: '待审核（未生效）', APPROVED: '已批准', RETIRED: '已停用' };
export const SCHEDULE_STATUS_ZH: Record<string, string> = { NORMAL: '正常', DUE_SOON: '即将到期', DUE: '到期', OVERDUE: '逾期', INSUFFICIENT_DATA: '数据不足', DRAFT: '待审核（未生效）' };
export const STAGE_ZH: Record<string, string> = {
  LEAD_90: '提前 90 天提醒', LEAD_30: '提前 30 天提醒', LEAD_7: '提前 7 天提醒', DUE: '已到期', OVERDUE: '已逾期', PCT_80: '使用量 80%', PCT_90: '使用量 90%', PCT_100: '使用量 100%',
};
export const ALERT_STATUS_ZH: Record<string, string> = { OPEN: '待处理', WORK_ORDER: '已生成工单', CLOSED: '已关闭' };
export const WO_STATUS_ZH: Record<string, string> = { OPEN: '待分配', ASSIGNED: '已分配', IN_PROGRESS: '执行中', COMPLETED: '已完成', CANCELLED: '已取消' };
export const WO_TYPE_ZH: Record<string, string> = { PREVENTIVE: '预防性维护', CALIBRATION: '校准', INSPECTION: '检查', CORRECTIVE: '纠正性维修', REPLACEMENT: '更换' };
export const WO_RESULT_ZH: Record<string, string> = { DONE: '已完成', DONE_WITH_FINDINGS: '完成（有发现）', NOT_DONE: '未完成' };
export const PRIORITY_ZH: Record<string, string> = { LOW: '低', MEDIUM: '中', HIGH: '高', CRITICAL: '紧急' };
export const GAP_ZH: Record<string, string> = { TIME: '日期条件', METER: '计量条件' };
export const CONCLUSION_ZH: Record<string, string> = {
  CONTINUE: '继续使用', CONTINUE_MONITOR: '继续使用并加强监测', REPLACE_RECOMMENDED: '建议更换（由责任人决定）', STOP_PENDING_REVIEW: '停用待复核',
};
export const INSPECTION_TYPE_ZH: Record<string, string> = { DAILY: '日常检查', VISUAL: '目视检查', PERIODIC: '定期检查', MRT: '磁探检测', OTHER: '其他' };

// History (structured events → Chinese; the persisted audit / revision rows are never rewritten)
export const HISTORY_CATEGORY_ZH: Record<string, string> = { LEDGER: '台账变更', REPLACEMENT: '器件更换', WORK_ORDER: '工单流转', WIRE_ROPE: '钢丝绳检查', FAULT: '故障记录' };
export const ENTITY_TYPE_ZH: Record<string, string> = {
  asset: '资产', equipment: '器件', location: '位置', relation: '设备关系', catalog: '设备目录', manufacturer: '制造商', project: '项目',
  asset_pending_attribute: '待确认属性', maintenance_rule: '维护规则', equipment_runtime_binding: '运行身份绑定',
};
export const FIELD_ZH: Record<string, string> = {
  created: '新建', imported: '导入', installedAt: '实际安装日期', status: '状态', confirmedValue: '确认值', approvalStatus: '审批状态', effectiveFrom: '生效日期',
  ratedCapacityT: '额定起重量', spanM: '跨度', latestInspectionDate: '最近检验日期', inspectionValidUntil: '检验有效期', useRegistrationNo: '使用登记号',
  displayName: '名称', assetStatus: '资产状态', location: '位置', removed: '拆卸', retired: '退役', moved: '移动', updated: '修改', disabled: '停用',
};
/** 「器件 新建」 from an entity_revisions summary "equipment created,…" (unknown parts → 字段变更). */
export function ledgerSummaryZh(summary: string): string {
  const [entity, fields = ''] = summary.split(' ');
  const parts = fields.split(',').filter(Boolean).map((f) => FIELD_ZH[f] ?? '字段变更');
  return `${ENTITY_TYPE_ZH[entity] ?? '记录'}${parts.length ? ' · ' + [...new Set(parts)].join('、') : ''}`;
}
/** 「SIM-WO-… 已分配 → 执行中（备注）」 from "CODE FROM→TO（note）". */
export function workOrderSummaryZh(summary: string): string {
  const m = summary.match(/^(\S+) ([A-Z_]*)→([A-Z_]+)(.*)$/);
  if (!m) return summary;
  const from = m[2] ? WO_STATUS_ZH[m[2]] ?? '未知状态' : '创建';
  return `${m[1]} ${from} → ${WO_STATUS_ZH[m[3]] ?? '未知状态'}${m[4] ?? ''}`;
}
/** Rope history "PERIODIC CONTINUE_MONITOR：finding". */
export function ropeSummaryZh(summary: string): string {
  const m = summary.match(/^([A-Z_]+) ([A-Z_]+)(.*)$/);
  return m ? `${INSPECTION_TYPE_ZH[m[1]] ?? '检查'} · ${CONCLUSION_ZH[m[2]] ?? '结论未知'}${m[3]}` : summary;
}
export const FAULT_CODE_ZH: Record<string, string> = { SIM_TEST_FAULT: '模拟故障（验收用）' };
export function faultSummaryZh(summary: string): string {
  const i = summary.indexOf(' ');
  const code = i > 0 ? summary.slice(0, i) : summary;
  const text = i > 0 ? summary.slice(i + 1) : '';
  return `${FAULT_CODE_ZH[code] ?? '故障'}${text ? '：' + text : ''}（故障码 ${code}）`;
}
export function historySummaryZh(category: string, summary: string): string {
  switch (category) {
    case 'LEDGER': return ledgerSummaryZh(summary);
    case 'WORK_ORDER': return workOrderSummaryZh(summary);
    case 'WIRE_ROPE': return ropeSummaryZh(summary);
    case 'FAULT': return faultSummaryZh(summary);
    default: return summary;
  }
}

// Review issues and pending attributes
export const ISSUE_ZH: Record<string, string> = {
  DUPLICATE_PLC_ADDRESS_GROUP: '重复 PLC 地址组', MISSING_BIT_INDEX: '地址缺少位索引', THERMAL_RELAY_HOURS_VS_COUNT: '热继统计口径（次数 / 小时）不一致',
  UNIT_CONFLICT_HOURS_VS_TONNES: '吨数单位写为「小时」', CHECK_RAW_BIT_VS_PUBLISHED_METRIC_TYPE: '开关量信号与整数 / 浮点发布值需核对',
  ONE_SIGNAL_MULTIPLE_DERIVED_METERS: '一个信号派生多个指标', SHARED_HOIST_BRAKE_SOURCE: '钢丝绳使用量借用起升抱闸信号（代理量）',
  ROPE_COUNT_AND_INSTALLATION_UNKNOWN: '钢丝绳根数与安装日期未知', INSPECTION_REQUIRED_NO_AUTO_DISCARD: '需人工检查，系统不判定报废',
  DUPLICATE_ADDRESS_DIFFERENT_EQUIPMENT: '同一地址对应不同设备', DUPLICATE_ADDRESS_MAY_BE_SHARED_STATUS: '同一地址可能是共用状态', ADDRESS_SHARED_REVIEW_REQUIRED: '共用地址需复核',
  NON_PLC_SOURCE_UNKNOWN: '非 PLC 来源未知', LIDAR_2D_OR_3D_UNCONFIRMED: '激光雷达二维 / 三维未确认', SOURCE_NOT_SPECIFIED: '数据来源未注明',
  ASSET_INSTALLATION_DATES_AND_BASELINES_UNKNOWN: '安装日期与初始累计值未知', MODEL_INTERPRETATION_PENDING_NAMEPLATE: '型号解读待铭牌确认',
};
export const ISSUE_STATUS_ZH: Record<string, string> = { OPEN: '待处理', RESOLVED: '已处理', ACCEPTED: '已接受现状' };
export const PENDING_ATTR_ZH: Record<string, string> = {
  ratedCapacityT: '额定起重量（吨）', spanM: '跨度（米）', location: '现场位置', statutoryInspection: '使用登记与法定检验', ropeCount: '钢丝绳根数',
  lidarDimension: '激光雷达维度', rfidEntity: 'RFID 实体类型', radarType: '雷达类型',
};
export const PENDING_STATUS_ZH: Record<string, string> = { PENDING: '待确认', CONFIRMED: '已确认', REJECTED: '已否决' };
/** A pending candidate value in Chinese (RFID_READER → RFID 读写器, numbers with their unit). */
export function pendingValueZh(attribute: string, v: unknown): string {
  if (v === null || v === undefined) return '未知';
  if (typeof v === 'string') return EQUIPMENT_TYPE_TEXT[v] ?? v;
  if (typeof v === 'number') return `${v}${attribute === 'ratedCapacityT' ? ' 吨' : attribute === 'spanM' ? ' 米' : ''}`;
  if (typeof v === 'object') {
    const o = v as Record<string, unknown>;
    if ('inspectionValidUntil' in o) return `有效期至 ${String(o.inspectionValidUntil ?? '—')}`;
    return Object.entries(o).map(([k, x]) => `${FIELD_ZH[k] ?? k}：${String(x)}`).join('；');
  }
  return String(v);
}
/** Mechanism group names come from the source Excel (already Chinese); slot codes → mechanism. */
export const MECHANISM_BY_SLOT_PREFIX: Record<string, string> = { GANTRY: '大车机构', TROLLEY: '小车机构', HOIST: '起升机构', GRAB: '抓斗机构', ACCESS: '门禁系统', ACQ: '数采系统', AUTO: '自动作业' };
export const mechanismOfSlot = (slot: string | null | undefined): string => (slot ? MECHANISM_BY_SLOT_PREFIX[slot.split('-')[0]] ?? '其他' : '整机');

// Personnel / schedule / intake (pages own their richer texts; these are the shared fallbacks)
export const INTAKE_STATUS_ZH: Record<string, string> = { REGISTERED: '已登记', REVIEW: '评审中', PLANNING: '规划中', APPROVED: '已批准', REJECTED: '已驳回', CANCELLED: '已取消' };

// ---------------------------------------------------------------------------------------------
// Errors — Chinese by ApiError code / HTTP status / business field. Server English never becomes the
// main message; an unknown write outcome stays UNKNOWN (NetworkWriteError text), never "failed".
// ---------------------------------------------------------------------------------------------
const CODE_ZH: Record<string, string> = {
  NO_PERMISSION: '没有执行此操作的权限。',
  NOT_FOUND: '记录不存在，或您无权查看。',
  VERSION_CONFLICT: '数据已被他人修改，请刷新核对最新内容后再操作。',
  PRECONDITION_REQUIRED: '缺少版本信息，请刷新页面后重试。',
  DATABASE_UNAVAILABLE: '服务暂时不可用，请稍后重试。',
  IDENTITY_PROVIDER: '身份服务暂时不可用，操作可稍后重试。',
  LAST_SYSTEM_ADMIN: '至少需要保留一名启用状态的系统管理员。',
  PERSON_BUSY: '该人员的另一项账号操作正在进行，请稍后刷新再试。',
  PROVISION_INCOMPLETE: '该账号的开户尚未完成，请在“未完成的开户”中继续或放弃。',
  SELF: '不能对自己的账号执行此操作，请由另一位系统管理员处理。',
  ARCHIVED: '该人员已删除（归档），不能再执行此操作。',
  REFERENCED: '该人员存在业务历史记录，只能保留为已删除（归档），不能永久删除。',
  NOT_TEST_ACCOUNT: '只有经过显式标记的测试账号才能永久删除；正式员工只能删除（归档）。',
  ACCOUNT_SERVICE_NOT_CONFIGURED: '本部署未配置账号服务。',
  IDEMPOTENCY_KEY: '请求重复提交且内容不同，请刷新后重新提交。',
};
/** Known server phrases → Chinese (field-level validation the user can act on). */
const MESSAGE_ZH: [RegExp, string][] = [
  [/basis .*required|basis \(what was done\) required/i, '请填写依据说明（至少 2 个字）。'],
  [/reason .*required/i, '请填写原因（2–200 字）。'],
  [/assignedTo required/i, '请填写责任人。'],
  [/cancellation reason required/i, '取消工单需要填写备注。'],
  [/replacement reason required/i, '请填写更换原因。'],
  [/inspector's conclusion is required/i, '请选择检查人的结论（系统不自动判定）。'],
  [/needs a verified DB address/i, 'PLC 点位需填写经现场核实的地址（如 DB505.DBX32.0 或 DB505.DBW10）。'],
  [/needs its active level/i, '开关量点位需填写有效电平。'],
  [/needs the counted edge/i, '计次点位需填写计数边沿。'],
  [/open review issue\(s\) of this point must be settled/i, '确认点位前，须一并处理该点位的全部核查事项。'],
  [/is not an open issue of this point/i, '所选核查事项已处理或不属于该点位，请刷新后重试。'],
  [/installedAt must be a past date/i, '安装日期不能晚于今天。'],
  [/latestInspectionDate .*inspectionValidUntil/i, '最近检验日期不能晚于今天，有效期须晚于最近检验日期。'],
  [/must be a positive number/i, '请填写大于 0 的数值。'],
  [/a confirmed value is required/i, '请填写确认值。'],
  [/initialUsage .* required|initialUsage must be/i, '请填写初始使用量（新件填 0）和读数依据。'],
  [/baseline already recorded/i, '该器件的初始计量基准已登记，更正需走审核流程。'],
  [/intervals must be positive|interval required for its type/i, '请按规则类型填写大于 0 的周期。'],
  [/effectiveFrom required/i, '请填写生效日期。'],
  [/rule is not this unapproved real rule version/i, '该规则已被审批或已更新，请刷新后核对。'],
  [/no pending decrease to confirm/i, '该计量当前没有待确认的计数下降，无需确认复位。'],
  [/changed \(version \d+\)|changed by someone else|current instance changed/i, '数据已被他人修改，请刷新核对最新内容后再操作。'],
  [/invalid work order transition/i, '工单当前状态不允许此操作，请刷新后核对。'],
  [/is immutable/i, '设备类型或目录项不能直接修改，如需更换请拆卸后重新安装。'],
  [/simulation \/ test records cannot change the real ledger/i, '模拟 / 测试记录不能修改真实台账。'],
  [/active runtime binding/i, '该器件存在活动的运行身份绑定，不能执行此操作。'],
  [/already removed/i, '该器件已拆卸。'],
  [/retired asset cannot be modified|asset status is terminal/i, '资产已退役，不能再修改。'],
  [/asset code already exists/i, '该项目中已存在相同的资产编号。'],
  [/location code already exists/i, '该项目中已存在相同的位置编码。'],
  [/disabled location cannot be an installation target/i, '已停用的位置不能作为安装位置。'],
  [/read-only through the business API|global catalog is read-only/i, '全局目录为只读，不能在业务界面修改。'],
  [/belongs to another project|crosses project boundary/i, '引用了其他项目的数据，不允许跨项目使用。'],
  [/equipment type .* (is disabled|not in the code registry|not initialized)/i, '该设备类型未启用或未初始化，请联系管理员。'],
  [/catalog item .* is disabled|catalog item not found/i, '所选设备目录项不存在或已停用。'],
  [/catalog type .* does not match/i, '设备目录项与设备类型不一致。'],
  [/displayName exceeds/i, '名称过长。'],
  [/idempotency/i, '请求重复提交且内容不同，请刷新后重新提交。'],
  [/^invalid body$/i, '提交的内容格式不正确，请检查后重试。'],
];

export type ErrorContext = 'load' | 'save';
/**
 * The main Chinese message for an error. NetworkWriteError keeps its own (already Chinese) text,
 * which distinguishes "not sent" from "outcome unknown" — never reported as plain failure.
 */
export function errorZh(e: unknown, context: ErrorContext = 'save'): string {
  if (e instanceof NetworkWriteError) return e.message;
  if (e instanceof ApiError) {
    const known = CODE_ZH[e.code];
    if (known && e.code !== 'VERSION_CONFLICT' && e.code !== 'NOT_FOUND') return known;
    const phrase = MESSAGE_ZH.find(([re]) => re.test(e.message));
    switch (e.status) {
      case 401: return '登录已失效，请重新登录。';
      case 403: return known ?? '没有执行此操作的权限。';
      case 404: return CODE_ZH.NOT_FOUND;
      case 409: return phrase?.[1] ?? CODE_ZH.VERSION_CONFLICT;
      case 422: case 400: return phrase?.[1] ?? (context === 'save' ? '输入不符合要求，请检查后重试。' : '请求参数不正确。');
      case 428: return CODE_ZH.PRECONDITION_REQUIRED;
      case 429: return '操作过于频繁，请稍后再试。';
    }
    if (e.status >= 500) return context === 'save' ? '服务暂时不可用，本次操作未被受理，可稍后重试。' : '服务暂时不可用，请稍后重试。';
    return phrase?.[1] ?? (context === 'save' ? '操作未完成，请稍后重试。' : '读取失败，请稍后重试。');
  }
  if (e instanceof TypeError || (e instanceof Error && /fetch|network/i.test(e.message))) return '网络连接异常，请检查网络后重试。';
  if (e instanceof Error && (e.name === 'TimeoutError' || e.name === 'AbortError')) return '请求超时，请稍后重试。';
  if (e instanceof Error && /[一-鿿]/.test(e.message)) return e.message;
  return context === 'save' ? '操作未完成，请稍后重试。' : '读取失败，请稍后重试。';
}
/**
 * A 422 / validation text for forms: the server text when it is already Chinese (field messages of
 * the personnel / account service), a known phrase in Chinese, otherwise a Chinese summary with
 * the offending field name kept in brackets as technical detail (never the raw English sentence).
 */
export function validationZh(message: string): string {
  if (/[一-鿿]/.test(message)) return message;
  const phrase = MESSAGE_ZH.find(([re]) => re.test(message));
  if (phrase) return phrase[1].replace(/。$/, '');
  const field = message.match(/\b([a-z][A-Za-z]+)\b (?:is required|must|exceeds|is invalid)/)?.[1]
    ?? message.match(/:\s*([a-z][A-Za-z]+)\b/)?.[1];
  return field ? `字段内容不符合要求（${field}）` : '提交内容不符合要求';
}

/** Technical detail for the 「详情」 line (server text, status, code) — secondary, never the headline. */
export function errorDetail(e: unknown): string {
  if (e instanceof ApiError) return `HTTP ${e.status} · ${e.code}${e.message ? ' · ' + e.message : ''}`;
  return '';
}
