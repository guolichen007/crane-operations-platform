// P1 personnel / account / permission management — API client and pure presentation logic.
//
// Three permission domains are shown side by side and must never be confused:
//   系统权限 (system_admin / account_manager)  — people, accounts, system & portfolio roles;
//   项目组合权限 (portfolio_planner / portfolio_manager) — register / approve / staff projects;
//   项目权限 (viewer / operator / maintainer / platform_admin) — one project's assets & devices.
// A system role never grants project or portfolio business access.
import { api, ApiError, NetworkWriteError } from './client';

export type SystemRole = 'system_admin' | 'account_manager';
export const SYSTEM_ROLES: SystemRole[] = ['system_admin', 'account_manager'];
export const SYSTEM_ROLE_TEXT: Record<SystemRole, { label: string; detail: string }> = {
  system_admin: { label: '系统管理员', detail: '人员与账号、系统角色、项目组合角色、部门；不含任何项目业务数据访问' },
  account_manager: { label: '账号管理员', detail: '为普通人员开户、编辑、启停用、重置密码、撤销会话；不能授予角色，不能操作系统角色持有人' },
};
export const PORTFOLIO_ROLES = ['portfolio_planner', 'portfolio_manager'] as const;
export type PortfolioRole = (typeof PORTFOLIO_ROLES)[number];
export const PORTFOLIO_ROLE_LABEL: Record<PortfolioRole, string> = { portfolio_planner: '项目组合规划者', portfolio_manager: '项目组合经理' };
export const PROJECT_ROLE_LABEL: Record<string, string> = { viewer: '只读', operator: '操作员', maintainer: '维护员', platform_admin: '项目管理员' };

export type SystemAccess = {
  userId: string;
  systemRoles: SystemRole[];
  capabilities: Record<string, boolean>;
  accountServiceConfigured: boolean;
};
export type GrantRef = { scopeId: string; scopeName: string; role: string };
export type Person = {
  id: string; username: string; displayName: string; departmentId: string; departmentName: string; jobTitle: string;
  phone: string; email: string; enabled: boolean; managed: boolean; identitySync: 'SYNCED' | 'PENDING';
  archivedAt: string | null; archivedBy: string; archiveReason: string; protected: '' | 'OWNER' | 'SERVICE' | 'SYSTEM';
  createdAt: string; createdBy: string; lastLoginAt: string | null; version: number; systemRoles: SystemRole[];
  projectCount: number; portfolioCount: number; portfolioGrants?: GrantRef[]; projectGrants?: GrantRef[];
};
export type HistoryEntry = { at: string; actor: string; action: string; result: string; scope: string; change: unknown; requestId: string; sourceIp: string };
export type PersonDetail = Person & {
  identity: { available: boolean; exists: boolean; enabled: boolean; passwordChangeRequired: boolean };
  history: HistoryEntry[];
};
export type Department = { id: string; name: string; status: 'ACTIVE' | 'DISABLED'; version: number; memberCount: number };
export type PortfolioScopeRef = { id: string; name: string; status: string };
export type Profile = { displayName: string; departmentId: string; jobTitle: string; phone: string; email: string };
export type ProvisionRequest = { username: string; profile: Profile; systemRoles: SystemRole[]; portfolioGrants: { scopeId: string; role: PortfolioRole }[] };
export type ProvisionResult = { provisionId: string; state: string; person: Person; temporaryPassword?: string; passwordDelivered: boolean; replayed: boolean };
export type AccountResult = { person: Person; identitySync: 'SYNCED' | 'PENDING' | 'UNMANAGED'; sessionsRevoked: number; temporaryPassword?: string };
export type Provision = { id: string; actor: string; username: string; state: string; failure: string; request: ProvisionRequest; createdAt: string; updatedAt: string };
export type ArchiveResult = AccountResult & { revoked: { systemRoles: string[]; portfolio: string[]; projects: string[] } };
export type PurgeCheck = { archived: boolean; protected: boolean; references: Record<string, number>; purgeable: boolean };
export type Candidate = { userId: string; displayName: string; username: string; departmentName: string; jobTitle: string };

export const getSystemAccess = () => api<SystemAccess>('admin/me');
export const listPeople = (q: { q?: string; departmentId?: string; status?: string; systemRole?: string; limit?: number; offset?: number }) =>
  api<{ items: Person[]; total: number }>('admin/people', { query: q });
export const getPerson = (id: string) => api<PersonDetail>(`admin/people/${encodeURIComponent(id)}`);
export const provisionPerson = (req: ProvisionRequest, idempotencyKey: string) =>
  api<ProvisionResult>('admin/people', { method: 'POST', body: req, idempotencyKey });
export const updatePerson = (id: string, version: number, p: Profile) =>
  api<AccountResult>(`admin/people/${encodeURIComponent(id)}`, { method: 'PATCH', body: p, ifMatch: version });
export const setEnabled = (id: string, version: number, enabled: boolean) =>
  api<AccountResult>(`admin/people/${encodeURIComponent(id)}/${enabled ? 'enable' : 'disable'}`, { method: 'POST', ifMatch: version });
export const resetPassword = (id: string) => api<AccountResult>(`admin/people/${encodeURIComponent(id)}/reset-password`, { method: 'POST' });
export const revokeSessions = (id: string) => api<AccountResult>(`admin/people/${encodeURIComponent(id)}/revoke-sessions`, { method: 'POST' });
export const setSystemRoles = (id: string, roles: SystemRole[], expectedRoles: SystemRole[]) =>
  api<Person>(`admin/people/${encodeURIComponent(id)}/system-roles`, { method: 'PUT', body: { roles, expectedRoles } });
export const setPortfolioRole = (id: string, scope: string, role: PortfolioRole | null, expectedRole: string | null) =>
  api<Person>(`admin/people/${encodeURIComponent(id)}/portfolio-roles/${encodeURIComponent(scope)}`, { method: 'PUT', body: { role, expectedRole } });
export const archivePerson = (id: string, version: number, reason: string, confirmUsername: string) =>
  api<ArchiveResult>(`admin/people/${encodeURIComponent(id)}/archive`, { method: 'POST', body: { reason, confirmUsername }, ifMatch: version });
export const purgeCheck = (id: string) => api<PurgeCheck>(`admin/people/${encodeURIComponent(id)}/purge-check`);
export const purgePerson = (id: string, confirmUsername: string) =>
  api<void>(`admin/people/${encodeURIComponent(id)}/purge`, { method: 'POST', body: { confirmUsername } });
export const listDepartments = () => api<Department[]>('admin/departments');
export const createDepartment = (d: { id: string; name: string }) => api<Department>('admin/departments', { method: 'POST', body: { ...d, status: 'ACTIVE', version: 0, memberCount: 0 } });
export const updateDepartment = (d: Department) =>
  api<Department>(`admin/departments/${encodeURIComponent(d.id)}`, { method: 'PUT', body: { id: d.id, name: d.name, status: d.status, version: d.version, memberCount: 0 }, ifMatch: d.version });
export const listAdminPortfolioScopes = () => api<PortfolioScopeRef[]>('admin/portfolio-scopes');
export const listProvisions = () => api<Provision[]>('admin/provisions');
export const resumeProvision = (id: string) => api<ProvisionResult>(`admin/provisions/${encodeURIComponent(id)}/resume`, { method: 'POST' });
export const cancelProvision = (id: string) => api<void>(`admin/provisions/${encodeURIComponent(id)}/cancel`, { method: 'POST' });
export const searchMemberCandidates = (projectId: string, q: string) =>
  api<Candidate[]>(`portfolio/projects/${encodeURIComponent(projectId)}/member-candidates`, { query: { q } });

// ---------------------------------------------------------------------------------------------
// Validation (mirrors internal/personnel; the server re-validates everything)
// ---------------------------------------------------------------------------------------------
export const USERNAME = /^[a-z0-9][a-z0-9_-]{1,62}$/;
const RESERVED = new Set(['admin', 'root', 'bootstrap-crane', 'system', 'anonymous']);

export function validateBasic(username: string, p: Profile): Record<string, string> {
  const e: Record<string, string> = {};
  const u = username.trim().toLowerCase();
  if (!u) e.username = '请填写登录用户名';
  else if (!USERNAME.test(u)) e.username = '用户名需为 2–63 位小写字母、数字、- 或 _，并以字母或数字开头';
  else if (RESERVED.has(u) || u.startsWith('service-account-')) e.username = '该用户名为系统保留名称';
  Object.assign(e, validateProfile(p));
  return e;
}

export function validateProfile(p: Profile): Record<string, string> {
  const e: Record<string, string> = {};
  const name = p.displayName.trim();
  if (!name) e.displayName = '请填写姓名';
  else if ([...name].length > 80) e.displayName = '姓名不能超过 80 个字符';
  if ([...p.jobTitle.trim()].length > 80) e.jobTitle = '岗位不能超过 80 个字符';
  if (p.phone.trim() && !/^[0-9+() -]{3,32}$/.test(p.phone.trim())) e.phone = '电话格式不正确';
  if (p.email.trim() && !/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(p.email.trim())) e.email = '邮箱格式不正确';
  return e;
}

export function personnelErrorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.code === 'LAST_SYSTEM_ADMIN') return '操作被拒绝：至少需要保留一名启用状态的系统管理员。';
    if (error.code === 'SELF') return '不能对自己的账号执行此操作，请由另一位系统管理员处理。';
    if (error.code === 'IDENTITY_PROVIDER') return '身份服务暂不可用，操作未完成，可稍后重试（已输入的内容已保留）。';
    if (error.code === 'ACCOUNT_SERVICE_NOT_CONFIGURED') return '本部署尚未配置账号服务，无法开户或重置密码，请联系运维。';
    if (error.code === 'PERSON_BUSY') return '该人员的另一项账号操作正在进行，请稍后刷新再试。';
    if (error.code === 'PROVISION_INCOMPLETE') return '该账号的开户尚未完成，请在「未完成的开户」中继续或放弃。';
    if (error.code === 'IDEMPOTENCY_KEY') return '提交内容与上一次不一致，请刷新后重新填写。';
    if (error.code === 'ARCHIVED') return '该人员已删除（归档），不能再执行此操作。';
    if (error.code === 'REFERENCED') return '该人员存在业务历史记录，只能保留为已删除（归档），不能永久删除。';
    switch (error.status) {
      case 401: return '登录已失效，请重新登录。';
      case 403: return '没有执行此操作的系统权限（或目标人员持有您无权管理的系统角色）。';
      case 404: return '人员或部门不存在，可能已被其他管理员修改。';
      case 409: return /username|用户名/i.test(error.message) ? '该登录用户名已存在，请换一个。' : `数据已被他人修改或已是目标状态：${error.message}`;
      case 422: return error.message;
      case 428: return '缺少版本信息，请刷新后重试。';
      default: return `请求失败（${error.status}）：${error.message}`;
    }
  }
  if (error instanceof NetworkWriteError) return error.message;
  return '网络连接失败或请求超时，请刷新核对后再操作。';
}

export function statusLabel(p: Pick<Person, 'enabled' | 'managed' | 'identitySync'> & { archivedAt?: string | null }): { text: string; tone: 'ok' | 'off' | 'warn' } {
  if (p.archivedAt) return { text: '已删除（归档）', tone: 'off' };
  if (!p.enabled) return { text: '已停用', tone: 'off' };
  if (p.identitySync === 'PENDING') return { text: '启用 · 身份同步待重试', tone: 'warn' };
  if (!p.managed) return { text: '启用 · 账号未纳管', tone: 'warn' };
  return { text: '启用', tone: 'ok' };
}

export const ACTION_TEXT: Record<string, string> = {
  personnel_created: '开户', account_enabled: '启用账号', account_disabled: '停用账号', password_reset: '重置密码',
  sessions_revoked: '撤销会话', profile_updated: '修改资料', system_role_changed: '调整系统角色', portfolio_role_changed: '调整项目组合角色',
  project_role_changed: '调整项目权限', account_adopted: '宿主机纳管', account_provision_failed: '开户失败', account_provision_cancelled: '开户已取消',
  login_success: '登录', logout: '退出', identity_bound: '绑定身份',
  personnel_archived: '删除人员（归档）', personnel_purged: '永久删除测试账号', identity_protection_changed: '调整受保护标记',
};

/** Purge reference keys → user-facing names (why a person can only stay archived). */
export const REFERENCE_TEXT: Record<string, string> = {
  audit_actions: '操作日志', entity_revisions: '台账修订', project_intakes: '项目登记责任人', intake_transitions: '项目登记流转',
  project_activities: '项目活动负责人', person_resources: '人员资源', resource_reservations: '资源预约', conflict_overrides: '冲突放行',
  schedule_baselines: '排期审批', accounts_opened: '开户操作', people_created: '创建的人员', roles_granted: '授予的系统角色',
  maintenance_work_orders: '维护工单', maintenance_rules: '维护规则', wire_rope_inspections: '钢丝绳检查', onboarding_imports: '台账导入',
  point_mapping_reviews: '点位核查', equipment_replacements: '器件更换', meter_baselines: '计量基准',
};

export const systemRoleNames = (roles: readonly string[]): string =>
  roles.map((r) => (SYSTEM_ROLE_TEXT as Record<string, { label: string }>)[r]?.label ?? r).join('、');
export const portfolioRoleName = (role: string): string => (PORTFOLIO_ROLE_LABEL as Record<string, string>)[role] ?? role;

export function formatTime(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return '—';
  return d.toLocaleString('zh-CN', { hour12: false, timeZone: 'Asia/Shanghai' });
}
