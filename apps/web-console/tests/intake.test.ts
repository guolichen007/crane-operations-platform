import { describe, expect, it } from 'vitest';
import {
  blankIntakeForm, formDirty, validateIntakeForm, toIntakeInput, intakeErrorMessage, listState, intakeStages, dateToInstant,
} from '../src/cloud/intake';
import { ApiError } from '../src/cloud/client';
import type { ProjectIntake } from '../../../contracts/typescript/types.gen';

const filled = () => ({ ...blankIntakeForm('PS-A'), intakeCode: 'INT-1', projectName: '项目', projectType: 'NEW_INSTALLATION' });
const intake = (over: Partial<ProjectIntake> = {}): ProjectIntake => ({
  id: 'I1', portfolioScopeId: 'PS-A', tenantId: 'demo', intakeCode: 'INT-1', projectName: '项目', customerName: null, projectType: 'NEW_INSTALLATION',
  priority: 'MEDIUM', riskLevel: 'MEDIUM', scopeSummary: '', siteReference: null, registeredAt: '2026-11-10T00:00:00Z', registeredBy: 'u',
  requestedStart: null, requestedHandover: null, plannedStart: null, plannedEnd: null, status: 'REGISTERED',
  projectManagerUserId: null, technicalLeadUserId: null, siteLeadUserId: null, formalProjectId: null, version: 1, ...over,
});

describe('项目登记表单', () => {
  it('必填、长度、枚举、范围与日期顺序都在提交前校验', () => {
    expect(validateIntakeForm(filled(), ['PS-A'])).toEqual({});
    const e = validateIntakeForm({ ...blankIntakeForm(''), projectName: '   ', priority: 'URGENT', plannedStart: '2026-12-02', plannedEnd: '2026-12-01' }, ['PS-A']);
    expect(Object.keys(e).sort()).toEqual(['intakeCode', 'plannedEnd', 'portfolioScopeId', 'priority', 'projectName', 'projectType'].sort());
    expect(validateIntakeForm({ ...filled(), portfolioScopeId: 'PS-X' }, ['PS-A']).portfolioScopeId).toBeTruthy();
    expect(validateIntakeForm({ ...filled(), projectName: '长'.repeat(161) }, ['PS-A']).projectName).toMatch(/160/);
    expect(validateIntakeForm({ ...filled(), requestedStart: '2026-12-01', requestedHandover: '2026-12-01' }, ['PS-A']).requestedHandover).toBeTruthy();
  });
  it('提交体与契约字段一一对应：未填为 null，不伪造人员或日期', () => {
    const body = toIntakeInput({ ...filled(), customerName: '  ', plannedStart: '2026-12-01' });
    expect(Object.keys(body).sort()).toEqual(['customerName', 'intakeCode', 'plannedEnd', 'plannedStart', 'portfolioScopeId', 'priority', 'projectManagerUserId',
      'projectName', 'projectType', 'requestedHandover', 'requestedStart', 'riskLevel', 'scopeSummary', 'siteLeadUserId', 'siteReference', 'technicalLeadUserId'].sort());
    expect(body.customerName).toBeNull();
    expect(body.plannedEnd).toBeNull();
    expect(body.projectManagerUserId).toBeNull();
    expect(body.plannedStart).toBe(new Date('2026-12-01T00:00:00').toISOString());
    expect(dateToInstant('')).toBeNull();
  });
  it('只有改动过才算草稿；初始范围不算改动', () => {
    expect(formDirty(blankIntakeForm('PS-A'), 'PS-A')).toBe(false);
    expect(formDirty({ ...blankIntakeForm('PS-A'), projectName: 'x' }, 'PS-A')).toBe(true);
  });
});

describe('错误提示', () => {
  it('403/409/422/503/网络各有明确含义，不会显示为空数据', () => {
    expect(intakeErrorMessage(new ApiError(403, 'NO_PERMISSION', 'x'), 'approve')).toMatch(/经理/);
    expect(intakeErrorMessage(new ApiError(403, 'NO_PERMISSION', 'x'), 'create')).toMatch(/登记权限/);
    expect(intakeErrorMessage(new ApiError(409, 'VERSION_CONFLICT', 'x'), 'create')).toMatch(/登记编号/);
    expect(intakeErrorMessage(new ApiError(422, 'X', 'invalid intake input: projectName is required'), 'create')).toMatch(/projectName/);
    expect(intakeErrorMessage(new ApiError(503, 'DATABASE_UNAVAILABLE', 'x'), 'create')).toMatch(/已保留/);
    expect(intakeErrorMessage(new TypeError('Failed to fetch'), 'create')).toMatch(/网络/);
    // List failures never claim a form input was kept.
    expect(intakeErrorMessage(new ApiError(503, 'DATABASE_UNAVAILABLE', 'x'), 'load')).not.toMatch(/已保留/);
  });
  it('列表状态区分 loading / error / stale / empty / ready', () => {
    expect(listState({ isLoading: true, isError: false, data: undefined })).toBe('loading');
    expect(listState({ isLoading: false, isError: true, data: undefined })).toBe('error');
    expect(listState({ isLoading: false, isError: true, data: [1] })).toBe('stale');
    expect(listState({ isLoading: false, isError: false, data: [] })).toBe('empty');
    expect(listState({ isLoading: false, isError: false, data: [1] })).toBe('ready');
  });
});

describe('后续环节说明只基于 API 事实', () => {
  it('未批准时不声称生成正式项目', () => {
    expect(intakeStages(intake({ status: 'PLANNING' }), [], undefined).map((s) => s.key)).toEqual(['review']);
    expect(intakeStages(intake({ status: 'REJECTED' }), [], undefined)[0].tone).toBe('stopped');
  });
  it('已批准 ≠ 已授权 ≠ 已进入地图；无服务端事实时显示「未知」', () => {
    const approved = intake({ status: 'APPROVED', formalProjectId: 'FP1' });
    const formal = { inventoryRegistered: false, adminCode: null, grantStatus: 'NO_MEMBERS' as const, memberCount: 0 };
    const s = intakeStages(approved, [], formal);
    expect(s.map((x) => [x.key, x.tone])).toEqual([['formal', 'ok'], ['members', 'pending'], ['grant', 'pending'], ['map', 'pending']]);
    expect(s[1].text).toMatch(/不会自动授权/);
    // Formal project not loaded / request failed / not visible → map is UNKNOWN, not "not on map".
    const unknown = intakeStages(approved, [], undefined);
    expect(unknown.find((x) => x.key === 'map')).toMatchObject({ tone: 'unknown' });
    expect(unknown.find((x) => x.key === 'map')!.text).not.toMatch(/未进入/);
    expect(intakeStages(approved, null, formal).find((x) => x.key === 'grant')!.tone).toBe('unknown');
    const granted = intakeStages(approved, [{ projectId: 'FP1', projectName: 'x', role: 'viewer' }], { ...formal, grantStatus: 'MEMBERS_CONFIGURED', memberCount: 2 });
    expect(granted.find((x) => x.key === 'members')!.text).toMatch(/2 名/);
    expect(granted.find((x) => x.key === 'grant')!.tone).toBe('ok');
    expect(intakeStages(approved, [], { ...formal, inventoryRegistered: true }).find((x) => x.key === 'map')!.text).toMatch(/空间位置/);
    expect(intakeStages(approved, [], { ...formal, inventoryRegistered: true, adminCode: 330481 }).find((x) => x.key === 'map')!.tone).toBe('ok');
  });
});
