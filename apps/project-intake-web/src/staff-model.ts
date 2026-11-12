import type { AccountOperation } from './staff-account';
export const roles: Record<string, string> = {
  baobei_sales: '销售',
  baobei_manager: '主管',
  baobei_director: '总监',
  baobei_admin: '管理员',
};

export const statuses: Record<string, string> = {
  PENDING_PROVISION: '待开通',
  PENDING_ACTIVATION: '待首次登录',
  ACTIVE: '已启用',
  SUSPENDED: '已停用',
  PROVISION_FAILED: '开通失败',
  ARCHIVED: '已归档',
};

// Status shown in lists: "待开通" splits into "awaiting review" and "in progress"
// once an administrator approved this exact version.
export function statusLabel(m: Pick<StaffMember, 'status' | 'provisionReviewed'> & Partial<Pick<StaffMember, 'accountState' | 'accountOperation'>>) {
  if (m.accountState === 'REVIEW' || m.accountOperation?.state === 'REVIEW') return '待核对';
  if (m.accountOperation?.state === 'STARTED') return '处理中';
  if (m.accountState === 'FAILED' && m.status !== 'SUSPENDED') return '开通失败';
  if (m.accountState === 'NONE' && m.status === 'PENDING_PROVISION') return '待开通';
  return m.status === 'PENDING_PROVISION' && m.provisionReviewed ? '待身份核验' : statuses[m.status] ?? m.status;
}

// The next step for each state, in the words of the person who must act.
export function nextStep(m: Pick<StaffMember, 'status' | 'provisionReviewed' | 'syncStatus'> & Partial<Pick<StaffMember, 'accountState' | 'mustChangePassword' | 'accountOperation'>>,
  admin: boolean, accountService = false): string {
  if (accountService) {
    if (m.accountState === 'REVIEW' || m.accountOperation?.state === 'REVIEW') return admin ? '账号状态待核对，登录已暂停：请核对账号状态' : '等待管理员核对账号状态';
    if (m.accountOperation?.state === 'STARTED') return admin ? '账号操作处理中，系统会自动核对；稍后刷新' : '等待管理员处理';
    if (m.status === 'SUSPENDED') return admin ? '可恢复登录或归档' : '';
    if (m.accountState === 'FAILED') return admin ? '开通未完成，可重新开通' : '等待管理员处理';
    if (m.accountState === 'NONE' || m.accountState === 'CREATING') return admin ? '请开通登录账号' : '等待管理员开通登录账号';
    if (m.status === 'PENDING_ACTIVATION' || m.mustChangePassword) return '等待成员用手机号和初始密码首次登录并修改密码';
    return '';
  }
  switch (m.status) {
    case 'PENDING_PROVISION':
      if (!admin) return '等待管理员审核开通';
      return m.provisionReviewed ? '审核已通过，等待企业身份核验' : '请审核开通';
    case 'PENDING_ACTIVATION': return '等待成员首次登录完成激活';
    case 'PROVISION_FAILED': return admin ? '查看失败原因后重试' : '等待管理员处理';
    case 'SUSPENDED': return m.syncStatus === 'FAILED' ? '停用同步失败，请重试' : '可申请恢复或归档';
    case 'ACTIVE': return '';
    default: return '';
  }
}

export const capabilities: Record<string, string> = {
  'project.create': '新建报备',
  'project.submit': '提交报备',
  'activity.create': '记录跟进',
};

export const syncStatuses: Record<string, string> = {
  PENDING: '待处理',
  ACKNOWLEDGED: '已核对',
  FAILED: '处理未完成',
  SUPERSEDED: '已由新请求替代',
  NONE: '无待处理任务',
};

export const staffEvents: Record<string, string> = {
  'staff.invite': '添加成员',
  'staff.edit': '编辑资料',
  'staff.role-change': '提交角色授权变更',
  'staff.permissions': '调整业务权限',
  'staff.suspend': '停用账号',
  'staff.reactivate': '申请恢复',
  'staff.retry': '重新排队',
  'staff.archive': '归档账号',
  'staff.transfer': '移交项目',
  'staff.activate': '企业账号激活',
  'staff.provision.approve': '企业身份核验并绑定',
  'staff.provision.ack-suspend': '核对停用回执',
  'staff.provision.fail': '登记处理失败',
  'staff.provision.review': '审核开通（业务批准）',
  'staff.account.create': '开通登录账号',
  'staff.account.reset-password': '重置登录密码',
  'staff.account.change-phone': '更换登录手机号',
  'staff.account.disable': '停用登录',
  'staff.account.enable': '恢复登录',
  'staff.account.sync-role': '同步登录角色',
  'staff.account.failed': '账号操作未完成',
  'staff.account.review': '账号状态待核对',
};

export interface StaffMember {
  id: string;
  name: string;
  email: string;
  phone: string;
  teamId: string;
  team: string;
  role: string;
  status: string;
  version: number;
  note: string;
  permissions: string[];
  projectCount: number;
  lastLogin: string | null;
  syncStatus: string;
  canManage: boolean;
  identityLinked: boolean;
  identityVerified: boolean;
  roleChanged: boolean;
  provisionReviewed: boolean;
  accountState: string;
  mustChangePassword: boolean;
  loginName?: string;
  accountOperation?: AccountOperation | null;
}

export interface StaffProject {
  id: string;
  number: string;
  name: string;
  status: string;
  version: number;
  queued: boolean;
}

export interface StaffTeam {
  id: string;
  name: string;
  version: number;
}

export interface StaffEvent {
  actor: string;
  action: string;
  reason: string;
  version: number;
  at: string;
}
