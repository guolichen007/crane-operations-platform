import { describe, expect, it } from 'vitest';
import { accountCause, accountDisplayState, awaitsRoleSync } from './staff-account';
import { nextStep, statusLabel } from './staff-model';

// G4-S5-R1-F: an unfinished or unconfirmed account operation is never shown as ready.
describe('account display state', () => {
  const base = { status: 'PENDING_ACTIVATION', provisionReviewed: false, syncStatus: 'NONE', mustChangePassword: true };
  const op = (state: 'STARTED' | 'FAILED' | 'COMPENSATED' | 'REVIEW', detail = '') =>
    ({ id: 'x', operation: 'CREATE', state, step: 'ENABLE', detail, at: '2026-11-12T00:00:00Z' });

  it('shows 待核对 for REVIEW, never 已开通', () => {
    const m = { ...base, accountState: 'REVIEW', accountOperation: op('REVIEW', 'COMPENSATION_FAILED') };
    expect(accountDisplayState(m)).toBe('REVIEW');
    expect(statusLabel(m)).toBe('待核对');
    expect(nextStep(m, true, true)).toContain('核对账号状态');
    expect(nextStep(m, false, true)).toContain('等待管理员');
  });

  it('shows 处理中 while an operation is running, even if the account was READY', () => {
    const m = { ...base, accountState: 'READY', accountOperation: op('STARTED') };
    expect(accountDisplayState(m)).toBe('PROCESSING');
    expect(statusLabel(m)).toBe('处理中');
  });

  it('shows the failure cause with a retry', () => {
    const m = { ...base, status: 'PROVISION_FAILED', accountState: 'FAILED', accountOperation: op('COMPENSATED', 'PHONE_TAKEN') };
    expect(statusLabel(m)).toBe('开通失败');
    expect(nextStep(m, true, true)).toContain('重新开通');
    expect(accountCause('PHONE_TAKEN')).toContain('已被其他登录账号使用');
    expect(accountCause('RESULT_LOST')).toContain('重新重置');
    expect(accountCause('SOMETHING_NEW')).toBe('操作未完成');
  });

  it('a finished account is ready', () => {
    expect(accountDisplayState({ accountState: 'READY', accountOperation: null })).toBe('READY');
  });
});

// G5-C-U1: HTTP 200 of a promotion / re-verification is not "in effect".
describe('role change after submit', () => {
  const submitted = { status: 'PENDING_PROVISION', provisionReviewed: false, syncStatus: 'PENDING', accountState: 'READY',
    identityLinked: true, mustChangePassword: false, lastLogin: '2026-11-16T00:00:00Z', accountOperation: null };
  it('submitted with a login: 待身份处理, only the administrator syncs the role', () => {
    expect(awaitsRoleSync(submitted)).toBe(true);
    expect(statusLabel(submitted)).toBe('待身份处理');
    expect(nextStep(submitted, true, true)).toContain('同步登录角色');
    expect(nextStep(submitted, true, true)).toContain('尚未生效');
    expect(nextStep(submitted, false, true)).toContain('等待管理员');
  });
  it('no sync entry without a login, while running or under review', () => {
    expect(awaitsRoleSync({ ...submitted, accountState: 'NONE' })).toBe(false);
    expect(awaitsRoleSync({ ...submitted, identityLinked: false })).toBe(false);
    expect(awaitsRoleSync({ ...submitted, status: 'ACTIVE' })).toBe(false);
    for (const state of ['STARTED', 'REVIEW'] as const)
      expect(awaitsRoleSync({ ...submitted, accountOperation: { id: 'x', operation: 'SET_ROLE', state, step: 'ROLE', detail: '', at: '' } })).toBe(false);
  });
  it('after the sync: 待本人重新登录, then 已启用 after the own login', () => {
    const synced = { ...submitted, status: 'PENDING_ACTIVATION', syncStatus: 'SUPERSEDED' };
    expect(statusLabel(synced)).toBe('待本人重新登录');
    expect(nextStep(synced, true, true)).toContain('本人重新登录');
    expect(statusLabel({ ...synced, status: 'ACTIVE' })).toBe('已启用');
    // A brand-new account still asks for the first login with the initial password.
    expect(nextStep({ ...synced, lastLogin: null, mustChangePassword: true }, true, true)).toContain('初始密码');
  });
});
