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

// V1.1 A1/A7: one "项目中心" entry; list and board are two views of it (the
// old /pipeline path stays a valid bookmark and highlights the same entry).
export const PROJECT_CENTER = '项目中心';
/** Nav entry that is active for a path (/pipeline belongs to the project center). */
export function activeEntry(path: string) {
  if (path === '/pipeline' || path.startsWith('/projects/')) return '/projects';
  return path;
}
export function navigation(u: User | null | undefined): NavItem[] {
  if (!u || u.claimRequired) return [];
  const items: NavItem[] = [
    { path: '/', label: '工作台', icon: 'dashboard', group: 'workspace' },
    { path: '/followups', label: '跟进中心', icon: 'clock', group: 'workspace' },
    { path: '/projects', label: PROJECT_CENTER, icon: 'folder', group: 'projects' },
  ];
  items.push({ path: '/customers', label: '客户中心', icon: 'building', group: 'projects' });
  if (can(u, 'project.unassigned'))
    items.push({ path: '/unassigned', label: '待分配草稿', icon: 'inbox', group: 'projects' });
  // Discarded drafts of the people who may discard (own sales drafts, admin).
  if (can(u, 'project.create'))
    items.push({ path: '/recycle-bin', label: '草稿回收站', icon: 'trash', group: 'projects' });
  if (can(u, 'project.review')) {
    items.push({ path: '/reviews', label: '审核中心', icon: 'check', group: 'governance' });
    items.push({ path: '/duplicates', label: '重复处理', icon: 'copy', group: 'governance' });
  }
  items.push({ path: '/statistics', label: '数据分析', icon: 'chart', group: 'governance' });
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
  projects: '项目',
  governance: '审核与分析',
  organization: '组织',
};

// Route guard rules, expressed as required server capabilities.
export function routeAllowed(u: User | null | undefined, path: string): boolean {
  if (!u) return false;
  // V1.1 R3: until the name is confirmed only the profile page is reachable.
  if (u.claimRequired) return path === '/claim';
  if (path === '/claim') return false;
  if (u.profileCompleted === false) return path === '/profile';
  if (path === '/reviews' || path === '/duplicates') return can(u, 'project.review');
  if (path === '/unassigned') return can(u, 'project.unassigned');
  if (path === '/recycle-bin') return can(u, 'project.create');
  if (path.startsWith('/team/members')) return can(u, 'staff.manage');
  if (path.startsWith('/settings/users')) return can(u, 'staff.manage') && u.role === 'baobei_admin';
  if (path.startsWith('/settings/teams')) return can(u, 'team.manage');
  if (path === '/new') return can(u, 'project.create');
  return true;
}
