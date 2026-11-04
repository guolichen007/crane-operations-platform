import { describe, it, expect } from 'vitest';
import { canWriteProject, writableProjects, isViewerOnly } from '../src/cloud/auth-state';
import type { WebSession } from '../../../contracts/typescript/types.gen';

function session(roles: Array<[string, string]>): WebSession {
  return {
    userId: 'u1',
    displayName: '测试',
    csrfToken: 'csrf',
    expiresAt: '2099-01-01T00:00:00Z',
    idleExpiresAt: '2099-01-01T00:00:00Z',
    projects: roles.map(([projectId, role]) => ({
      projectId,
      projectName: projectId,
      role: role as WebSession['projects'][number]['role'],
    })),
  };
}

// Frozen matrix: only platform_admin holds asset_master_write. operator keeps
// watch+maintenance_request_create; maintainer adds inspection/maintenance etc.
// but stays asset-master read-only.
describe('资产权限矩阵（与服务端 capability.go 冻结角色一致）', () => {
  it('viewer 只读', () => {
    expect(canWriteProject(session([['P001', 'viewer']]), 'P001')).toBe(false);
    expect(writableProjects(session([['P001', 'viewer']]))).toHaveLength(0);
  });
  it('operator 资产只读（保留运行权限，非资产管理员）', () => {
    expect(canWriteProject(session([['P001', 'operator']]), 'P001')).toBe(false);
    expect(writableProjects(session([['P001', 'operator']]))).toHaveLength(0);
  });
  it('maintainer 资产只读（维保权限不受影响，非资产管理员）', () => {
    expect(canWriteProject(session([['P001', 'maintainer']]), 'P001')).toBe(false);
    expect(writableProjects(session([['P001', 'maintainer']]))).toHaveLength(0);
  });
  it('platform_admin 仅对已授权项目有资产写权限', () => {
    const s = session([
      ['P001', 'platform_admin'],
      ['P002', 'viewer'],
    ]);
    expect(canWriteProject(s, 'P001')).toBe(true);
    expect(canWriteProject(s, 'P002')).toBe(false);
    expect(writableProjects(s).map((p) => p.projectId)).toEqual(['P001']);
  });
  it('零授权用户没有可写项目', () => {
    expect(canWriteProject(session([]), 'P001')).toBe(false);
    expect(writableProjects(session([]))).toHaveLength(0);
    expect(isViewerOnly(session([]))).toBe(false);
  });
  it('多项目混合角色按项目隔离', () => {
    const s = session([
      ['P001', 'viewer'],
      ['P002', 'platform_admin'],
      ['P003', 'operator'],
    ]);
    expect(canWriteProject(s, 'P001')).toBe(false);
    expect(canWriteProject(s, 'P002')).toBe(true);
    expect(canWriteProject(s, 'P003')).toBe(false);
  });
});
