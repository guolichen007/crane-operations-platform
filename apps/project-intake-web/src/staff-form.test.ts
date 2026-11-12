import { describe, expect, it, beforeEach } from 'vitest';
import { normalizePhone, validateStaff, type StaffFormValues } from './staff-form';
import { clearStaffDraft, readStaffDraft, saveStaffDraft, type StaffDraft } from './staff-role-draft';

const base: StaffFormValues = { name: '张三', phone: '13800001111', email: '', role: 'baobei_sales', teamId: 't', reason: '' };
const rules = { phoneRequired: true, reasonRequired: false };

describe('staff phone', () => {
  it('normalizes mainland and international numbers like the server', () => {
    expect(normalizePhone('138 0000 1111')).toBe('+8613800001111');
    expect(normalizePhone('008613800001111')).toBe('+8613800001111');
    expect(normalizePhone('+86-138-0000-1111')).toBe('+8613800001111');
    expect(normalizePhone('+1 (415) 555-0100')).toBe('+14155550100');
    for (const bad of ['', '1380000111', '12800001111', '+8612800001111', 'abc', '+1234567'])
      expect(normalizePhone(bad)).toBeNull();
  });
});

describe('staff form validation', () => {
  it('accepts a minimal new member without email', () => {
    expect(validateStaff(base, rules)).toEqual({});
  });
  it('reports each field near its input', () => {
    const e = validateStaff({ name: '123', phone: '12', email: 'x@', role: 'baobei_manager', teamId: '', reason: '' },
      { phoneRequired: true, reasonRequired: true });
    expect(Object.keys(e).sort()).toEqual(['email', 'name', 'phone', 'reason', 'teamId']);
    expect(e.name).toContain('数字');
  });
  it('allows legacy members without phone and global roles without team', () => {
    expect(validateStaff({ ...base, phone: '', role: 'baobei_director', teamId: '' }, { phoneRequired: false, reasonRequired: false })).toEqual({});
    expect(validateStaff({ ...base, phone: '' }, rules).phone).toBe('请填写工作手机');
  });
});

describe('reauth draft', () => {
  const store = new Map<string, string>();
  beforeEach(() => {
    store.clear();
    globalThis.sessionStorage = {
      getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => void store.set(k, v),
      removeItem: (k: string) => void store.delete(k), clear: () => store.clear(), key: () => null, length: 0,
    } as Storage;
  });
  const draft = (over: Partial<StaffDraft> = {}): StaffDraft => ({
    actor: 'a', staffId: '', action: 'invite', version: 0, expiresAt: Date.now() + 60_000,
    form: { name: '张三', phone: '13800001111', email: '', teamId: 't', role: 'baobei_manager', note: '', reason: '任命', permissions: [] },
    ...over,
  });
  it('restores a valid invitation draft and expires old or tampered ones', () => {
    saveStaffDraft(draft());
    expect(readStaffDraft()?.form.name).toBe('张三');
    saveStaffDraft(draft({ expiresAt: Date.now() - 1 }));
    expect(readStaffDraft()).toBeNull();
    saveStaffDraft(draft({ expiresAt: Date.now() + 3_600_000 }));
    expect(readStaffDraft()).toBeNull(); // an extended window is refused
    saveStaffDraft(draft({ action: 'edit', staffId: 'not-a-uuid', version: 1 }));
    expect(readStaffDraft()).toBeNull();
    store.set('baobei.staffReauthDraft', '{broken');
    expect(readStaffDraft()).toBeNull();
    clearStaffDraft();
  });
});

import { revocable } from './staff-model';
describe('撤销开户入口（R2-P1）', () => {
  const base = { status: 'PENDING_PROVISION', lastLogin: null, projectCount: 0, canManage: true, accountState: 'NONE' };
  it('仅未完成开户、无登录、无项目、无未决账号操作时可撤销', () => {
    expect(revocable(base)).toBe(true);
    expect(revocable({ ...base, status: 'PROVISION_FAILED', accountState: 'FAILED' })).toBe(true);
    expect(revocable({ ...base, status: 'PENDING_ACTIVATION', accountState: 'READY' })).toBe(true);
    expect(revocable({ ...base, status: 'ACTIVE' })).toBe(false);
    expect(revocable({ ...base, lastLogin: '2026-11-01T00:00:00Z' })).toBe(false);
    expect(revocable({ ...base, projectCount: 1 })).toBe(false);
    expect(revocable({ ...base, canManage: false })).toBe(false);
    expect(revocable({ ...base, accountState: 'REVIEW' })).toBe(false);
    expect(revocable({ ...base, accountOperation: { id: 'x', operation: 'CREATE', state: 'STARTED', step: 'ROLE', detail: '', at: '' } })).toBe(false);
  });
});

import { accountCause } from './staff-account';
describe('撤销开户待核对原因（R2-P1-F1）', () => {
  it('每种待核对原因都有可操作的说明，不显示为成功', () => {
    for (const code of ['OWNER_MISMATCH', 'REVOKE_DELETE_FAILED', 'LOGIN_STILL_OCCUPIED', 'PROVIDER_UNAVAILABLE', 'REVOKE_UNCONFIRMED']) {
      const text = accountCause(code);
      expect(text).not.toBe('操作未完成');
      expect(text).not.toMatch(/已释放|已撤销/);
    }
    expect(accountCause('REVOKE_DELETE_FAILED')).toContain('手机号暂不释放');
  });
});
