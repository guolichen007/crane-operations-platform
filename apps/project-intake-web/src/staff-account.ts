// G4-S5-R1-E: administrator-managed login accounts (dedicated Baobei realm).
export type AccountAction = 'account-create' | 'account-reset' | 'account-phone' | 'account-disable' | 'account-enable';

// Dialog kind → API action under /api/v1/staff/{id}/account/{action}.
export const accountActions: Record<AccountAction, string> = {
  'account-create': 'create',
  'account-reset': 'reset-password',
  'account-phone': 'change-phone',
  'account-disable': 'disable',
  'account-enable': 'enable',
};

export const accountStateLabel: Record<string, string> = {
  NONE: '未开通',
  CREATING: '开通中',
  READY: '可登录',
  DISABLED: '已停用',
  FAILED: '开通失败',
};

export function newIdempotencyKey() {
  return 'acct-' + crypto.randomUUID().replace(/-/g, '');
}

// 11-digit mainland login name from the canonical +86 phone (display only).
export function loginNameOf(phone: string) {
  return /^\+861[3-9]\d{9}$/.test(phone) ? phone.slice(3) : '';
}
