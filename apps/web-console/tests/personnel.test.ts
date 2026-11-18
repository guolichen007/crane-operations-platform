import { describe, expect, it } from 'vitest';
import { validateBasic, validateProfile, statusLabel, personnelErrorMessage, systemRoleNames } from '../src/cloud/personnel';
import { validateGrantForm } from '../src/cloud/project-grants';
import { ApiError } from '../src/cloud/client';

const profile = { displayName: '张三', departmentId: '', jobTitle: '', phone: '', email: '' };

describe('personnel validation (mirrors internal/personnel)', () => {
  it('requires name and a well-formed, non-reserved username', () => {
    expect(validateBasic('zhangsan', profile)).toEqual({});
    expect(validateBasic('', profile).username).toBeTruthy();
    expect(validateBasic('A', profile).username).toBeTruthy();
    expect(validateBasic('x y', profile).username).toBeTruthy();
    expect(validateBasic('admin', profile).username).toContain('保留');
    expect(validateBasic('service-account-crane-bff', profile).username).toContain('保留');
    expect(validateBasic('ok-1', { ...profile, displayName: ' ' }).displayName).toBeTruthy();
  });
  it('checks optional contact formats only when present', () => {
    expect(validateProfile({ ...profile, phone: '138 0000 0000' })).toEqual({});
    expect(validateProfile({ ...profile, phone: 'abc' }).phone).toBeTruthy();
    expect(validateProfile({ ...profile, email: 'a@b.cn' })).toEqual({});
    expect(validateProfile({ ...profile, email: 'not-mail' }).email).toBeTruthy();
    expect(validateProfile({ ...profile, displayName: '名'.repeat(81) }).displayName).toBeTruthy();
  });
});

describe('personnel presentation', () => {
  it('status reflects disabled, pending identity sync and unmanaged accounts', () => {
    expect(statusLabel({ enabled: false, managed: true, identitySync: 'SYNCED' }).tone).toBe('off');
    expect(statusLabel({ enabled: true, managed: true, identitySync: 'PENDING' }).text).toContain('同步');
    expect(statusLabel({ enabled: true, managed: false, identitySync: 'SYNCED' }).text).toContain('未纳管');
    expect(statusLabel({ enabled: true, managed: true, identitySync: 'SYNCED' }).tone).toBe('ok');
  });
  it('maps server answers to actionable messages', () => {
    expect(personnelErrorMessage(new ApiError(409, 'LAST_SYSTEM_ADMIN', 'x'))).toContain('至少需要保留');
    expect(personnelErrorMessage(new ApiError(409, 'VERSION_CONFLICT', 'username already exists'))).toContain('用户名已存在');
    expect(personnelErrorMessage(new ApiError(503, 'IDENTITY_PROVIDER', 'x'))).toContain('可稍后重试');
    expect(personnelErrorMessage(new ApiError(403, 'NO_PERMISSION', 'x'))).toContain('系统权限');
    expect(systemRoleNames(['system_admin', 'account_manager'])).toBe('系统管理员、账号管理员');
  });
  it('project grant form now requires a picked person (no raw id typing)', () => {
    expect(validateGrantForm({ userId: '', role: 'viewer' }, 'me').userId).toContain('选择');
  });
});
