import type { User } from './model';

// Navigation is derived from server-issued businessActions. It only decides
// which entries are shown; every endpoint re-authorizes on the server.
export interface NavItem {
  path: string;
  label: string;
  icon: string;
  group: 'workspace' | 'projects' | 'governance' | 'organization';
}
export const can = (u: User | null | undefined, action: string) =>
  !!u?.businessActions?.[action]?.allowed;

export function projectScopeLabel(u: User | null | undefined) {
  if (!u) return '项目';
  if (u.role === 'baobei_sales') return '我的报备';
  if (u.role === 'baobei_manager') return '团队项目';
  return '全部项目';
}
export function workspaceLabel(u: User | null | undefined) {
  return (
    {
      baobei_sales: '销售工作空间',
      baobei_manager: '主管工作空间',
      baobei_director: '总监工作空间',
      baobei_admin: '管理工作空间',
    }[u?.role ?? ''] ?? '工作空间'
  );
}
export function roleLabel(role: string) {
  return (
    { baobei_sales: '销售', baobei_manager: '销售主管', baobei_director: '销售总监', baobei_admin: '管理员' }[
      role
    ] ?? '成员'
  );
}

export function navigation(u: User | null | undefined): NavItem[] {
  if (!u) return [];
  const items: NavItem[] = [
    { path: '/', label: '工作台', icon: 'dashboard', group: 'workspace' },
    { path: '/followups', label: '待跟进', icon: 'clock', group: 'workspace' },
    { path: '/projects', label: projectScopeLabel(u), icon: 'folder', group: 'projects' },
  ];
  if (can(u, 'project.unassigned'))
    items.push({ path: '/unassigned', label: '待分配草稿', icon: 'inbox', group: 'projects' });
  items.push({ path: '/pipeline', label: '项目管道', icon: 'columns', group: 'projects' });
  // Discarded drafts of the people who may discard (own sales drafts, admin).
  if (can(u, 'project.create'))
    items.push({ path: '/recycle-bin', label: '草稿回收站', icon: 'trash', group: 'projects' });
  if (can(u, 'project.review')) {
    items.push({ path: '/reviews', label: '报备审核', icon: 'check', group: 'governance' });
    items.push({ path: '/duplicates', label: '重复处理', icon: 'copy', group: 'governance' });
  }
  items.push({ path: '/statistics', label: '统计', icon: 'chart', group: 'governance' });
  if (can(u, 'staff.manage'))
    items.push({
      path: u.role === 'baobei_admin' ? '/settings/users' : '/team/members',
      label: u.role === 'baobei_admin' ? '人员管理' : '团队成员',
      icon: 'users',
      group: 'organization',
    });
  if (can(u, 'team.manage'))
    items.push({ path: '/settings/teams', label: '团队管理', icon: 'settings', group: 'organization' });
  return items;
}
export const navGroups: Record<NavItem['group'], string> = {
  workspace: '工作',
  projects: '项目中心',
  governance: '审核与分析',
  organization: '组织',
};

// Route guard rules, expressed as required server capabilities.
export function routeAllowed(u: User | null | undefined, path: string): boolean {
  if (!u) return false;
  if (path === '/reviews' || path === '/duplicates') return can(u, 'project.review');
  if (path === '/unassigned') return can(u, 'project.unassigned');
  if (path === '/recycle-bin') return can(u, 'project.create');
  if (path.startsWith('/team/members')) return can(u, 'staff.manage');
  if (path.startsWith('/settings/users')) return can(u, 'staff.manage') && u.role === 'baobei_admin';
  if (path.startsWith('/settings/teams')) return can(u, 'team.manage');
  if (path === '/new') return can(u, 'project.create');
  return true;
}
