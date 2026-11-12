import { describe, expect, it } from 'vitest';
import { accountCause, accountDisplayState } from './staff-account';
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
