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
export function statusLabel(m: Pick<StaffMember, 'status' | 'provisionReviewed'>) {
  return m.status === 'PENDING_PROVISION' && m.provisionReviewed ? '开通中' : statuses[m.status] ?? m.status;
}

// The next step for each state, in the words of the person who must act.
export function nextStep(m: Pick<StaffMember, 'status' | 'provisionReviewed' | 'syncStatus'>, admin: boolean): string {
  switch (m.status) {
    case 'PENDING_PROVISION':
      if (!admin) return '等待管理员审核开通';
      return m.provisionReviewed ? '等待身份运维创建企业账号并登记' : '请审核开通';
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
  'staff.provision.approve': '核对开通回执',
  'staff.provision.ack-suspend': '核对停用回执',
  'staff.provision.fail': '登记处理失败',
  'staff.provision.review': '审核开通',
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
