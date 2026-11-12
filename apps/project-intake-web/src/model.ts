export interface Location {
  province: string;
  city: string;
  district: string;
  industrialPark: string;
  factoryName: string;
  workshop: string;
  addressDetail: string;
}
export type KnowledgeState = 'UNKNOWN' | 'KNOWN' | 'LEGACY_UNCONFIRMED';
export interface ActionHint { allowed: boolean; reasonCode: string | null }
export interface ReviewEligibility {
  projectReadable: boolean;
  projectIndependence: ActionHint;
  relatedCandidates: ActionHint;
  pendingDuplicates: number;
  legacyCandidates?: number;
  nextAction?: 'approve' | 'duplicate-decisions';
  eligible: boolean;
  reasonCode: string | null;
}
export interface Equipment {
 knowledge?: Record<string, KnowledgeState>;
 environmentFlags?: string[] | null;
 automationGoals?: string[] | null;
  equipmentType: string;
  quantity: number | null;
  manufacturer: string;
  model?: string;
  capacityTons?: number | null;
  spanM?: number | null;
  liftHeightM?: number | null;
  controlMode?: string;
  plcBrand?: string;
  plcModel?: string;
  driveBrand?: string;
  notes?: string;
  confidenceLevel?: string;
}
export interface Input {
 knowledge?: Record<string, KnowledgeState>;
 nextAction?: string;
 nextFollowupAt?: string | null;
  version: number;
  projectName: string;
  customer: string;
  location: Location;
  salesStage: string;
  projectType: string;
  informationSource: string;
  confidenceLevel: string;
  estimatedAmount: number | null;
  expectedPurchaseDate: string;
  requirements: string[];
  requirementNotes: string;
  plannedEquipment: Equipment[];
  parties: { role: string; name: string }[];
  tenderNo: string;
  notes: string;
}
export interface Activity {
  type: string;
  occurredAt: string;
  summary: string;
  result: string;
  nextAction: string;
  nextFollowupAt: string;
  actorName?: string;
}
export interface Project extends Input {
 documentHash: string;
 review?: ReviewEligibility;
 ownerName: string;
 createdByName: string;
 custodianName: string;
 submittedByName: string;
 replayed?: boolean;
 actions: Record<string, ActionHint>;
 teamId: string | null;
 draftCustodianSubject: string;
 submittedBySubject: string;
 submissionRevision: number;
 evidenceState: 'COMPLETE' | 'INCOMPLETE';
 assignmentStatus: 'ASSIGNED' | 'UNASSIGNED_DRAFT';
 protectionStartedAt: string | null;
 protectionBasis: string;
  protectedUntil: string | null;
  nextReviewAt: string | null;
  // Frozen submission time of the current round (review queue only).
  submittedAt?: string | null;
  governanceHistory?: {
    actor: string;
    actorName?: string;
    fromOwnerName?: string;
    toOwnerName?: string;
    action: string;
    reason: string;
    fromOwner?: string;
    toOwner?: string;
    at: string;
  }[];
  id: string;
  registrationNo: string;
  ownerSubject: string | null;
  createdBySubject: string;
  intakeStatus: string;
  // Set while the draft is in the recycle bin (status CANCELLED).
  discardedAt?: string | null;
  protectionStatus: string;
  createdAt: string;
  updatedAt: string;
  lastActivityAt: string | null;
  nextFollowupAt: string | null;
  nextAction: string;
  activities?: Activity[];
  audit?: {
    action: string;
    revision: number;
    createdAt: string;
    subject: string;
    actorName?: string;
    details: Record<string, unknown>;
  }[];
}
export interface User {
  subject: string;
  name: string;
  role: string;
  csrfToken: string;
  teamId: string;
  permissions: string[];
  businessActions?: Record<string, ActionHint>;
}
// G3-2A: the only disclosure about related candidates the viewer cannot read.
export interface RestrictedRelated {
  code: 'RESTRICTED_RELATED_PRESENT';
  message: string;
  route: 'SALES_DIRECTOR' | 'REVIEWER';
}
export interface DuplicateCheckResult {
  items: Duplicate[];
  restrictedRelated: RestrictedRelated | null;
}
export interface Duplicate {
  risk: string;
  score: number;
  region: string;
  createdAt: string;
  salesStage: string;
  message: string;
}
export const stages: Record<string, string> = {
  L0_LEAD: '线索',
  L1_REQUIREMENT: '需求确认',
  L2_TECHNICAL: '技术交流',
  L3_SITE_SURVEY: '现场勘察',
  L4_SOLUTION: '方案',
  L5_QUOTATION: '报价',
  L6_TENDER: '招投标',
  L7_COMMERCIAL: '商务沟通',
  L8_PENDING_CONTRACT: '待签约',
  WON: '已成交',
  LOST: '已失单',
  HOLD: '暂缓',
};
export const statuses: Record<string, string> = {
  DRAFT: '草稿',
  SUBMITTED: '已提交',
  UNDER_REVIEW: '重复待审核',
  APPROVED: '已批准',
  RETURNED: '退回补充',
  REJECTED: '已拒绝',
  EXPIRED: '已过期',
  CANCELLED: '已取消',
  CONVERTED: '已转换',
};
export const reqs: Record<string, string> = {
  AUTONOMOUS: '自动化',
  ANTI_SWAY: '防摇',
  COLLISION_AVOIDANCE: '防碰撞',
  POSITIONING: '定位',
  VISION: '视觉',
  LIDAR: '激光雷达',
  REMOTE_OPERATION: '远程操作需求',
  SAFETY: '安全',
  MONITORING: '监测',
  OTHER: '其他',
};
export const sources: Record<string, string> = {
  END_CUSTOMER: '终端客户',
  CRANE_MANUFACTURER: '行车厂家',
  DESIGN_INSTITUTE: '设计院',
  EPC: 'EPC',
  SYSTEM_INTEGRATOR: '集成商',
  AGENT: '代理',
  TENDER_PLATFORM: '招标平台',
  HISTORICAL_CUSTOMER: '历史客户',
  INTERNAL_REFERRAL: '内部介绍',
  OUTBOUND: '主动拓展',
  OTHER: '其他',
};
export const confidences: Record<string, string> = {
  RUMOR: '线索待核实',
  VERBAL: '口头信息',
  CUSTOMER_CONFIRMED: '客户确认',
  TECHNICAL_CONFIRMED: '技术确认',
  DOCUMENT_CONFIRMED: '文件确认',
  TENDER_CONFIRMED: '招标确认',
};
export const activityTypes: Record<string, string> = {
  PHONE: '电话',
  WECHAT: '微信',
  VISIT: '拜访',
  TECHNICAL: '技术交流',
  SITE_SURVEY: '现场勘察',
  SOLUTION: '方案',
  QUOTATION: '报价',
  TENDER: '招投标',
  COMMERCIAL: '商务沟通',
  OTHER: '其他',
};
export const blank = (): Input => ({
  version: 1,
  projectName: '',
  customer: '',
  location: {
    province: '',
    city: '',
    district: '',
    industrialPark: '',
    factoryName: '',
    workshop: '',
    addressDetail: '',
  },
  salesStage: 'L0_LEAD',
  projectType: '',
  informationSource: '',
  confidenceLevel: '',
  estimatedAmount: null,
  knowledge: {},
  nextAction: '',
  nextFollowupAt: null,
  expectedPurchaseDate: '',
  requirements: [],
  requirementNotes: '',
  plannedEquipment: [],
  parties: [],
  tenderNo: '',
  notes: '',
});
export const when = (v: string | null | undefined) =>
  v
    ? new Date(v).toLocaleString('zh-CN', {
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
      })
    : '—';

export const equipmentTypes: Record<string, string> = { BRIDGE: '桥式行车', GANTRY: '门式行车', OTHER: '其他起重设备' };
export const controlModes: Record<string, string> = {
  CAB: '司机室操作',
  PENDANT: '地面按钮',
  REMOTE: '无线遥控',
  SEMI_AUTO: '半自动',
  AUTO: '全自动',
  UNKNOWN: '尚不明确',
};
export const environments: Record<string, string> = {
  DUST: '粉尘',
  HIGH_TEMPERATURE: '高温',
  EXPLOSIVE: '防爆',
  OUTDOOR: '户外',
};
export const projectTypes: Record<string, string> = { NEW_BUILD: '新建项目', RETROFIT: '存量改造', EXPANSION: '扩建' };
export const blankEquipment = (): Equipment => ({
  equipmentType: 'BRIDGE',
  quantity: null,
  manufacturer: '',
  model: '',
  capacityTons: null,
  spanM: null,
  liftHeightM: null,
  controlMode: '',
  environmentFlags: [],
  automationGoals: [],
  notes: '',
  knowledge: {},
});
