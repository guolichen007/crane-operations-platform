import { describe, it, expect } from 'vitest';
import { navigation, routeAllowed } from './navigation';
import type { User } from './model';

const hint = (allowed: boolean) => ({ allowed, reasonCode: allowed ? null : 'BUSINESS_PERMISSION_DENIED' });
function user(role: string, actions: Record<string, boolean>): User {
  return {
    subject: 's', name: '合成', role, csrfToken: '', teamId: '', permissions: [],
    businessActions: Object.fromEntries(Object.entries(actions).map(([k, v]) => [k, hint(v)])),
  };
}
const paths = (u: User) => navigation(u).map((i) => i.path);

describe('服务端能力驱动导航', () => {
  it('管理员有业务工作台、待分配与组织入口', () => {
    const admin = user('baobei_admin', { 'project.create': true, 'project.review': true, 'project.unassigned': true, 'staff.manage': true, 'team.manage': true });
    expect(paths(admin)).toEqual(['/', '/followups', '/projects', '/unassigned', '/pipeline', '/recycle-bin', '/reviews', '/duplicates', '/statistics', '/settings/users', '/settings/teams']);
    expect(routeAllowed(admin, '/new')).toBe(true);
  });
  it('销售没有审核与组织入口', () => {
    const sales = user('baobei_sales', { 'project.create': true, 'project.review': false, 'staff.manage': false });
    expect(paths(sales)).not.toContain('/reviews');
    expect(paths(sales)).not.toContain('/unassigned');
    expect(routeAllowed(sales, '/reviews')).toBe(false);
    expect(routeAllowed(sales, '/settings/users')).toBe(false);
    expect(paths(sales)).toContain('/recycle-bin');
  });
  it('没有新建权限的角色看不到草稿回收站', () => {
    const manager = user('baobei_manager', { 'project.create': false, 'project.review': true, 'staff.manage': true });
    expect(paths(manager)).not.toContain('/recycle-bin');
    expect(routeAllowed(manager, '/recycle-bin')).toBe(false);
  });
  it('角色名本身不授予入口：缺少能力提示即隐藏', () => {
    const admin = user('baobei_admin', {});
    expect(paths(admin)).toEqual(['/', '/followups', '/projects', '/pipeline', '/statistics']);
    expect(routeAllowed(admin, '/new')).toBe(false);
    expect(routeAllowed(admin, '/unassigned')).toBe(false);
  });
  it('主管进入团队成员而非人员管理', () => {
    const manager = user('baobei_manager', { 'project.review': true, 'staff.manage': true });
    expect(paths(manager)).toContain('/team/members');
    expect(routeAllowed(manager, '/settings/users')).toBe(false);
    expect(routeAllowed(manager, '/settings/teams')).toBe(false);
  });
});
