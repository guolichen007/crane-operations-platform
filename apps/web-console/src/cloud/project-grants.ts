// Q2: formal projects + initial project grants — pure presentation / validation logic.
//
// Two different permission domains are shown side by side and must never be confused:
//   项目组合权限 (PortfolioGrant: portfolio_planner / portfolio_manager) — register, approve,
//     and (manager only) manage who may access the formal projects of that portfolio;
//   项目权限 (ProjectGrant: viewer / operator / maintainer / platform_admin) — access to ONE
//     project's assets/devices. Holding a portfolio role gives no project access.
import type { ProjectMember, ProjectGrantChange } from '../../../../contracts/typescript/types.gen';
import { ApiError } from './client';

export const PROJECT_ROLES = ['viewer', 'operator', 'maintainer', 'platform_admin'] as const;
export type ProjectRole = (typeof PROJECT_ROLES)[number];
export const PROJECT_ROLE_TEXT: Record<ProjectRole, { label: string; detail: string }> = {
  viewer: { label: '只读', detail: '查看项目、资产与运行数据' },
  operator: { label: '操作员', detail: '只读 + 查看实时数据、提交维修请求' },
  maintainer: { label: '维护员', detail: '操作员权限 + 点检、维修、钢丝绳与文档记录' },
  platform_admin: { label: '项目管理员', detail: '全部项目内权限：登记/修改/退役资产、位置与目录（不含设备控制）' },
};
export const PORTFOLIO_ROLE_TEXT: Record<string, string> = { portfolio_planner: '项目组合规划者', portfolio_manager: '项目组合经理' };

const USER_ID = /^[A-Za-z0-9_-]{1,80}$/;

export type GrantForm = { userId: string; role: ProjectRole | '' };

/** Field → message; empty = valid. The user id must be the exact platform account id. */
export function validateGrantForm(form: GrantForm, selfId: string | null): Partial<Record<keyof GrantForm, string>> {
  const e: Partial<Record<keyof GrantForm, string>> = {};
  const id = form.userId.trim();
  if (!id) e.userId = '必填：被授权人的平台账号 ID（精确值）';
  else if (!USER_ID.test(id)) e.userId = '账号 ID 只能包含字母、数字、- 和 _（不超过 80 位）';
  else if (selfId && id === selfId) e.userId = '不能为自己授予项目权限，请由另一位项目组合经理操作';
  if (!(PROJECT_ROLES as readonly string[]).includes(form.role)) e.role = '请选择项目角色';
  return e;
}

/** The change sent to the server, with the role the caller last saw as the concurrency guard. */
export function grantChange(userId: string, role: ProjectRole | null, members: ProjectMember[]): ProjectGrantChange {
  const current = members.find((m) => m.userId === userId)?.role ?? null;
  return { userId: userId.trim(), role, expectedRole: current as ProjectGrantChange['expectedRole'] };
}

export function grantErrorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    switch (error.status) {
      case 401: return '登录已失效，请重新登录。';
      case 403: return '无授权管理权限：需要该项目组合的「项目组合经理」角色。';
      case 404: return '项目不存在、未批准，或不属于您管理的项目组合。';
      case 409: return '成员权限已被他人修改或已是该状态，列表已刷新，请确认后再操作。';
      case 422: return `无法授权：${error.message.includes('identity provider') ? '账号不存在、已停用或不是本平台登录账号' : error.message}`;
      case 503: return '服务暂不可用，请稍后重试。已输入的内容已保留。';
      default: return `请求失败（${error.status}）：${error.message}`;
    }
  }
  return '网络连接失败或请求超时，请稍后重试。已输入的内容已保留。';
}

/** Map state from server facts; an unavailable Inventory answer is "unknown", never "not on map". */
export function mapStatus(project: { inventoryRegistered: boolean; adminCode: number | null }): { text: string; tone: 'ok' | 'pending' } {
  if (!project.inventoryRegistered) return { text: '未进入运营地图：尚未登记 Inventory 生命周期与行政区（下一阶段 Q3）', tone: 'pending' };
  if (project.adminCode === null) return { text: '已登记 Inventory，但尚未关联空间位置', tone: 'pending' };
  return { text: `已关联行政区 ${project.adminCode}`, tone: 'ok' };
}
