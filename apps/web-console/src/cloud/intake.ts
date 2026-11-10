// Project registration (ProjectIntake) form + presentation logic for PortfolioWorkspace.
// Pure functions only, so the rules are unit-testable without a browser.
//
// Relationship that the UI must never blur (verified in internal/portfolio + inventory):
//   ProjectIntake (登记)  --ApproveIntake-->  formal Project (projects row, lifecycle NULL)
//   - approval does NOT create a ProjectGrant (explicit authorization is a separate step);
//   - the formal project has no Inventory lifecycle / adminCode, so it is NOT in
//     /api/v1/projects, the operations summary or the national map;
//   - assets can only be registered after an explicit ProjectGrant with asset_master_write.
import type { InventoryProject, ProjectGrant, ProjectIntake, ProjectIntakeInput } from '../../../../contracts/typescript/types.gen';
import { ApiError } from './client';

export const PRIORITIES = ['LOW', 'MEDIUM', 'HIGH', 'CRITICAL'] as const;
export const RISK_LEVELS = ['LOW', 'MEDIUM', 'HIGH'] as const;
export const PRIORITY_TEXT: Record<string, string> = { LOW: '低', MEDIUM: '中', HIGH: '高', CRITICAL: '紧急' };
export const RISK_TEXT: Record<string, string> = { LOW: '低', MEDIUM: '中', HIGH: '高' };
/** Suggestions only: projectType is a free string (≤80) in the contract. */
export const PROJECT_TYPE_SUGGESTIONS = [
  { value: 'NEW_INSTALLATION', label: '新装' },
  { value: 'RETROFIT', label: '改造' },
  { value: 'MAINTENANCE', label: '维保' },
];

export type IntakeForm = {
  portfolioScopeId: string;
  intakeCode: string;
  projectName: string;
  projectType: string;
  priority: string;
  riskLevel: string;
  customerName: string;
  scopeSummary: string;
  siteReference: string;
  plannedStart: string; // yyyy-mm-dd from <input type="date">, '' = not set
  plannedEnd: string;
  requestedStart: string;
  requestedHandover: string;
};

/** MEDIUM/MEDIUM are the database column defaults, not invented values. */
export function blankIntakeForm(scopeId = ''): IntakeForm {
  return {
    portfolioScopeId: scopeId, intakeCode: '', projectName: '', projectType: '', priority: 'MEDIUM', riskLevel: 'MEDIUM',
    customerName: '', scopeSummary: '', siteReference: '', plannedStart: '', plannedEnd: '', requestedStart: '', requestedHandover: '',
  };
}

export function formDirty(form: IntakeForm, initialScope: string): boolean {
  return JSON.stringify(form) !== JSON.stringify(blankIntakeForm(initialScope));
}

const LIMITS: Partial<Record<keyof IntakeForm, number>> = {
  intakeCode: 80, projectName: 160, projectType: 80, customerName: 160, scopeSummary: 2000, siteReference: 160,
};

/** Field → message. Empty object = valid. Mirrors the server contract + CHECKs. */
export function validateIntakeForm(form: IntakeForm, scopeIds: string[]): Partial<Record<keyof IntakeForm, string>> {
  const e: Partial<Record<keyof IntakeForm, string>> = {};
  if (!scopeIds.includes(form.portfolioScopeId)) e.portfolioScopeId = '请选择有权限的项目组合';
  if (!form.intakeCode.trim()) e.intakeCode = '必填';
  if (!form.projectName.trim()) e.projectName = '必填';
  if (!form.projectType.trim()) e.projectType = '必填';
  for (const [k, max] of Object.entries(LIMITS) as [keyof IntakeForm, number][])
    if (!e[k] && [...form[k]].length > max) e[k] = `不超过 ${max} 字`;
  if (!(PRIORITIES as readonly string[]).includes(form.priority)) e.priority = '请选择优先级';
  if (!(RISK_LEVELS as readonly string[]).includes(form.riskLevel)) e.riskLevel = '请选择风险等级';
  if (form.plannedStart && form.plannedEnd && form.plannedEnd <= form.plannedStart) e.plannedEnd = '计划结束须晚于计划开始';
  if (form.requestedStart && form.requestedHandover && form.requestedHandover <= form.requestedStart) e.requestedHandover = '期望交付须晚于期望开工';
  return e;
}

/** Local calendar date at 00:00 → RFC 3339 instant; '' → null (never a fake date). */
export function dateToInstant(value: string): string | null {
  if (!value) return null;
  const d = new Date(`${value}T00:00:00`);
  return Number.isNaN(d.getTime()) ? null : d.toISOString();
}

const optional = (v: string): string | null => (v.trim() ? v.trim() : null);

/** Exact ProjectIntakeInput: every contract field present; unknown people stay null. */
export function toIntakeInput(form: IntakeForm): ProjectIntakeInput {
  return {
    portfolioScopeId: form.portfolioScopeId,
    intakeCode: form.intakeCode.trim(),
    projectName: form.projectName.trim(),
    customerName: optional(form.customerName),
    projectType: form.projectType.trim(),
    priority: form.priority as ProjectIntakeInput['priority'],
    riskLevel: form.riskLevel as ProjectIntakeInput['riskLevel'],
    scopeSummary: form.scopeSummary.trim(),
    siteReference: optional(form.siteReference),
    requestedStart: dateToInstant(form.requestedStart),
    requestedHandover: dateToInstant(form.requestedHandover),
    plannedStart: dateToInstant(form.plannedStart),
    plannedEnd: dateToInstant(form.plannedEnd),
    projectManagerUserId: null,
    technicalLeadUserId: null,
    siteLeadUserId: null,
  };
}

export type IntakeAction = 'create' | 'transition' | 'approve' | 'load';

/** User-facing message for an API failure. Never shown as "暂无数据". */
export function intakeErrorMessage(error: unknown, action: IntakeAction): string {
  const kept = action === 'create' ? '已输入的内容已保留。' : '';
  if (error instanceof ApiError) {
    switch (error.status) {
      case 400: return '请求格式错误，请刷新页面后重试。';
      case 401: return '登录已失效，请重新登录。';
      case 403:
        if (action === 'approve') return '无批准权限：批准需要该项目组合的「项目组合经理」角色。';
        if (action === 'load') return '无权限读取该数据。';
        return '无权限：您在该项目组合中没有项目登记权限。';
      case 404: return '记录不存在或您无权访问，列表已刷新。';
      case 409:
        if (action === 'create') return '该项目组合中已存在相同登记编号，请更换编号后再保存。';
        if (action === 'approve') return '状态已变化（可能已被他人批准或推进），列表已刷新。';
        return '状态流转无效或已被他人更新，列表已刷新。';
      case 422: return `输入不符合要求：${error.message}`;
      case 503: return `服务暂不可用（数据库或授权服务），请稍后重试。${kept}`;
      default: return `请求失败（${error.status}）：${error.message}`;
    }
  }
  return `网络连接失败或请求超时，请检查网络后重试。${kept}`;
}

/** loading / error / stale / empty / ready — a failure is never presented as empty data. */
export type ListState = 'loading' | 'error' | 'stale' | 'empty' | 'ready';
export function listState(q: { isLoading: boolean; isError: boolean; data: readonly unknown[] | undefined }): ListState {
  if (q.isError) return q.data ? 'stale' : 'error';
  if (q.isLoading || q.data === undefined) return 'loading';
  return q.data.length === 0 ? 'empty' : 'ready';
}

export type Stage = { key: string; text: string; tone: 'progress' | 'ok' | 'pending' | 'stopped' };

/**
 * What happens after this intake — only facts the API actually returns:
 * the intake status, its formalProjectId, the caller's own ProjectGrants and the
 * authorized Inventory project list (undefined = not loaded / not available).
 */
export function intakeStages(intake: ProjectIntake, grants: ProjectGrant[] | null, inventory: InventoryProject[] | undefined): Stage[] {
  if (intake.status === 'REJECTED' || intake.status === 'CANCELLED')
    return [{ key: 'stopped', text: intake.status === 'REJECTED' ? '已驳回，不会生成正式项目' : '已取消，不会生成正式项目', tone: 'stopped' }];
  if (intake.status !== 'APPROVED' || !intake.formalProjectId)
    return [{ key: 'review', text: '登记 / 评审中：尚未生成正式项目', tone: 'progress' }];
  const id = intake.formalProjectId;
  const stages: Stage[] = [{ key: 'formal', text: `已生成正式项目 ${id}`, tone: 'ok' }];
  const grant = grants?.find((g) => g.projectId === id);
  if (grants === null) stages.push({ key: 'grant', text: '项目授权状态未知（当前会话不提供）', tone: 'pending' });
  else if (!grant) stages.push({ key: 'grant', text: '您尚未获得该项目的访问 / 资产管理授权（审批不会自动授权，需管理员单独授予）', tone: 'pending' });
  else stages.push({ key: 'grant', text: `已获项目授权（${grant.role}）`, tone: 'ok' });
  if (!grant || inventory === undefined) {
    stages.push({ key: 'map', text: '未进入运营地图：正式项目需登记 Inventory 生命周期与行政区后才会出现在全国地图', tone: 'pending' });
  } else {
    const inv = inventory.find((p) => p.id === id);
    if (!inv) stages.push({ key: 'map', text: '未进入运营地图：尚未登记 Inventory 生命周期', tone: 'pending' });
    else if (inv.adminCode === null) stages.push({ key: 'map', text: '已在项目台账，但尚未关联空间位置（不进入地图钻取）', tone: 'pending' });
    else stages.push({ key: 'map', text: `已关联行政区 ${inv.adminCode}`, tone: 'ok' });
  }
  return stages;
}
