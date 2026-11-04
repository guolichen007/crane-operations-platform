const messages = new Map([
  ['LOGIN_CANCELLED', '您已取消企业登录，可以重新发起登录。'],
  ['LOGIN_PROVIDER_REJECTED', '企业认证未完成，请重新登录。如仍无法登录，请联系系统管理员。'],
  ['LOGIN_PROVIDER_UNAVAILABLE', '企业认证服务暂时不可用，请稍后重新登录。'],
  ['LOGIN_EXPIRED', '本次登录请求已过期，请重新登录。'],
  ['LOGIN_STATE_INVALID', '本次登录请求无效或已使用，请重新登录。请允许本站 Cookie。'],
  ['LOGIN_NONCE_INVALID', '无法确认本次企业登录，请重新登录。'],
  ['LOGIN_EXCHANGE_FAILED', '企业登录未能完成，请重新登录。'],
  ['LOGIN_TOKEN_INVALID', '无法验证企业身份，请重新登录。'],
  ['LOGIN_NOT_PROVISIONED', '您的报备账号尚未开通，请联系系统管理员。'],
  ['LOGIN_DISABLED', '您的报备账号已停用，请联系系统管理员。'],
  ['LOGIN_ROLE_MISSING', '您的企业账号缺少报备中心访问权限，请联系系统管理员。'],
  ['LOGIN_EMAIL_MISMATCH', '您的企业邮箱尚未验证或与开通信息不一致，请联系系统管理员。'],
  ['SESSION_UNAVAILABLE', '会话服务暂时不可用，请稍后重试。'],
  ['LOGIN_BUSY', '当前登录繁忙，请稍后重新登录。'],
]);

export function loginErrorMessage(code: unknown): string {
  return typeof code === 'string' ? messages.get(code) ?? '' : '';
}
