import { describe, expect, it } from 'vitest';
import { validateGrantForm, grantChange, grantErrorMessage, mapStatus, PROJECT_ROLES, PROJECT_ROLE_TEXT } from '../src/cloud/project-grants';
import { ApiError } from '../src/cloud/client';
import type { ProjectMember } from '../../../contracts/typescript/types.gen';

describe('项目授权表单', () => {
  it('精确账号 ID、合法角色；不能给自己授权', () => {
    expect(validateGrantForm({ userId: 'user-1', role: 'viewer' }, 'me')).toEqual({});
    expect(Object.keys(validateGrantForm({ userId: '', role: '' }, 'me')).sort()).toEqual(['role', 'userId']);
    expect(validateGrantForm({ userId: 'a b', role: 'viewer' }, 'me').userId).toMatch(/字母/);
    expect(validateGrantForm({ userId: 'me', role: 'viewer' }, 'me').userId).toMatch(/自己/);
    // Only the existing ProjectGrant role set; portfolio roles are not project roles.
    expect(validateGrantForm({ userId: 'u', role: 'portfolio_manager' as never }, null).role).toBeTruthy();
    expect([...PROJECT_ROLES]).toEqual(['viewer', 'operator', 'maintainer', 'platform_admin']);
    expect(PROJECT_ROLE_TEXT.platform_admin.detail).toMatch(/不含设备控制/);
  });
  it('提交带上调用者看到的角色作为并发保护', () => {
    const members: ProjectMember[] = [{ userId: 'u1', displayName: '', role: 'viewer', enabled: true }];
    expect(grantChange('u1', 'operator', members)).toEqual({ userId: 'u1', role: 'operator', expectedRole: 'viewer' });
    expect(grantChange(' u2 ', 'viewer', members)).toEqual({ userId: 'u2', role: 'viewer', expectedRole: null });
    expect(grantChange('u1', null, members)).toEqual({ userId: 'u1', role: null, expectedRole: 'viewer' });
  });
  it('403/404/409/422/503 各有明确提示', () => {
    expect(grantErrorMessage(new ApiError(403, 'NO_PERMISSION', 'x'))).toMatch(/项目组合经理/);
    expect(grantErrorMessage(new ApiError(404, 'NOT_FOUND', 'x'))).toMatch(/不属于您管理/);
    expect(grantErrorMessage(new ApiError(409, 'VERSION_CONFLICT', 'x'))).toMatch(/已被他人修改/);
    expect(grantErrorMessage(new ApiError(422, 'X', 'grant target is not an enabled user of this identity provider'))).toMatch(/已停用/);
    expect(grantErrorMessage(new ApiError(503, 'DATABASE_UNAVAILABLE', 'x'))).toMatch(/已保留/);
  });
  it('地图状态只来自服务端事实', () => {
    expect(mapStatus({ inventoryRegistered: false, adminCode: null }).text).toMatch(/未进入运营地图/);
    expect(mapStatus({ inventoryRegistered: true, adminCode: null }).text).toMatch(/空间位置/);
    expect(mapStatus({ inventoryRegistered: true, adminCode: 330481 }).tone).toBe('ok');
  });
});
