// G4-S5-R1-E: administrator-managed login accounts (dedicated Baobei realm).
export type AccountAction = 'account-create' | 'account-reset' | 'account-phone' | 'account-disable' | 'account-enable' | 'account-revoke';

// Dialog kind → API action under /api/v1/staff/{id}/account/{action}.
export const accountActions: Record<AccountAction, string> = {
  'account-create': 'create',
  'account-reset': 'reset-password',
  'account-phone': 'change-phone',
  'account-disable': 'disable',
  'account-enable': 'enable',
  'account-revoke': 'revoke',
};

export const accountStateLabel: Record<string, string> = {
  NONE: '未开通',
  CREATING: '开通中',
  READY: '可登录',
  DISABLED: '已停用',
  FAILED: '开通失败',
  REVIEW: '待核对',
};

// Latest account operation (server: accountOperation) and its cause, in words.
export interface AccountOperation {
  id: string;
  operation: string;
  state: 'STARTED' | 'FAILED' | 'COMPENSATED' | 'REVIEW';
  step: string;
  detail: string;
  at: string;
}

export const accountOperationLabel: Record<string, string> = {
  CREATE: '开通登录账号', RESET_PASSWORD: '重置密码', CHANGE_PHONE: '更换登录手机号',
  DISABLE: '停用登录', ENABLE: '恢复登录', SET_ROLE: '同步登录角色', REVOKE: '撤销开户',
};

const causes: Record<string, string> = {
  ACCOUNT_SERVICE_UNAVAILABLE: '账号服务暂时不可用',
  PROVIDER_UNAVAILABLE: '账号服务暂时不可用',
  PROVIDER_REFUSED: '账号服务拒绝了本次操作',
  PROVIDER_ERROR: '账号服务返回错误',
  PHONE_TAKEN: '该手机号已被其他登录账号使用',
  ACCOUNT_PHONE_TAKEN: '该手机号已被其他登录账号使用',
  STAFF_PHONE_DUPLICATE: '该工作手机已被其他成员使用',
  STAFF_CONFLICT: '成员记录已变化',
  RESULT_LOST: '结果未送达，密码可能已变更但无人知道，请重新重置',
  NOT_STARTED: '操作中断，未作任何更改',
  NOT_APPLIED: '操作中断，登录账号未变更',
  CREATE_OUTCOME_UNKNOWN: '无法确认账号是否已创建',
  COMPENSATION_FAILED: '撤销未完成的账号失败',
  USERNAME_OUTCOME_UNKNOWN: '无法确认登录手机号是否已更换',
  USERNAME_MISMATCH: '登录手机号与记录不一致',
  USERNAME_NOT_RESTORED: '登录手机号未能恢复',
  DISABLE_UNCONFIRMED: '无法确认登录已停用（报备中心已停止该成员访问）',
  ENABLE_UNCONFIRMED: '无法确认登录状态',
  ROLE_UNCONFIRMED: '无法确认登录角色',
  OWNER_MISMATCH: '账号归属与成员不一致',
  LOGIN_MISSING: '找不到登录账号',
  ACCOUNT_IDENTITY_CONFLICT: '该登录身份已对应其他成员，本次开通已撤销',
  SUBJECT_HAS_HISTORY: '该成员已有业务记录，登录身份保留不回退，登录已暂停',
  REVOKE_UNCONFIRMED: '无法确认登录账号已删除（报备中心已停止该成员访问）',
};

export function accountCause(detail: string) {
  return causes[detail] ?? (detail ? '操作未完成' : '');
}

// Display state: an unfinished or unconfirmed operation is never shown as ready.
export function accountDisplayState(m: { accountState: string; accountOperation?: AccountOperation | null }) {
  if (m.accountState === 'REVIEW' || m.accountOperation?.state === 'REVIEW') return 'REVIEW';
  if (m.accountOperation?.state === 'STARTED') return 'PROCESSING';
  return m.accountState;
}

export const accountDisplayLabel: Record<string, string> = { ...accountStateLabel, PROCESSING: '处理中' };

export function newIdempotencyKey() {
  return 'acct-' + crypto.randomUUID().replace(/-/g, '');
}

// 11-digit mainland login name from the canonical +86 phone (display only).
export function loginNameOf(phone: string) {
  return /^\+861[3-9]\d{9}$/.test(phone) ? phone.slice(3) : '';
}
