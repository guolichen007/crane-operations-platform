import { ApiError } from './api-client';
import type { ActionHint } from './model';

// G3-2A fixed notice: never carries a count, risk or any candidate detail.
export const RESTRICTED_RELATED_MESSAGE = '检测到需要跨组核验的相关项目。当前账号无权查看详情，请销售总监处理。';

// Fixed server reason codes rendered as plain guidance.
export const reasonText: Record<string, string> = {
  OWNER_REQUIRED: '需先指派一名有效销售负责人',
  OWNER_INACTIVE: '负责人已停用或团队已变化，请重新指派',
  BUSINESS_PERMISSION_DENIED: '当前账号没有这项业务权限',
  INVALID_STATE: '项目当前状态不允许此操作',
  NOT_FOUND: '项目不存在或不在你的范围内',
  REVIEW_CONFLICT: '你参与过此项目，需由其他独立审核人处理',
  REVIEW_EVIDENCE_INCOMPLETE: '历史提交证据不完整，需走独立核验流程',
  REVIEW_RELATED_PROJECT_RESTRICTED: RESTRICTED_RELATED_MESSAGE,
  DUPLICATE_DECISION_REQUIRED: '请先完成重复裁决',
  LEGACY_EVIDENCE_ACK_REQUIRED: '相关候选为历史数据、证据不完整，需确认责任声明后才能排除重复并批准',
  DUPLICATE_ALREADY_CONFIRMED: '已确认为重复报备，不能再次提交',
  VERSION_CONFLICT: '项目已被其他人更新',
  IDEMPOTENCY_CONFLICT: '本次创建与先前提交的内容不一致，请刷新后继续',
};
export const hintReason = (h: ActionHint | undefined, fallback = '暂不可用') =>
  h?.allowed ? '' : reasonText[h?.reasonCode ?? ''] || fallback;

export type Recovery = 'auth' | 'forbidden' | 'missing' | 'conflict' | 'retry' | 'input';
export function recovery(e: unknown): { kind: Recovery; message: string } {
  if (!(e instanceof ApiError)) return { kind: 'retry', message: e instanceof Error ? e.message : '操作未完成，请重试' };
  if (e.status === 401) return { kind: 'auth', message: '登录已失效，请重新登录后继续' };
  if (e.status === 403) return { kind: 'forbidden', message: reasonText[e.code] || '当前账号无权执行此操作，输入已保留' };
  if (e.status === 404) return { kind: 'missing', message: reasonText.NOT_FOUND };
  if (e.status === 409) return { kind: 'conflict', message: reasonText[e.code] || e.message };
  if (e.status === 400) return { kind: 'input', message: e.message };
  return { kind: 'retry', message: e.status === 0 ? e.message : e.message + '；输入已保留，可重试' };
}
