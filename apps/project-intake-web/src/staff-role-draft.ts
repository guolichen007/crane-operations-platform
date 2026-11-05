export type StaffRoleAction = 'promote' | 'demote';

export interface StaffRoleDraft {
  actor: string;
  staffId: string;
  action: StaffRoleAction;
  version: number;
  reason: string;
  expiresAt: number;
}

const key = 'baobei.staffRoleDraft';

export function saveStaffRoleDraft(draft: StaffRoleDraft) {
  sessionStorage.setItem(key, JSON.stringify(draft));
}

export function clearStaffRoleDraft() {
  try { sessionStorage.removeItem(key); } catch { /* Storage is optional. */ }
}

export function readStaffRoleDraft(): StaffRoleDraft | null {
  try {
    const raw = sessionStorage.getItem(key);
    if (!raw) return null;
    const draft = JSON.parse(raw) as StaffRoleDraft;
    if (typeof draft.actor === 'string' && /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(draft.staffId) &&
        (draft.action === 'promote' || draft.action === 'demote') &&
        Number.isSafeInteger(draft.version) && draft.version > 0 &&
        typeof draft.reason === 'string' && draft.reason.trim() && draft.reason.length <= 300 &&
        typeof draft.expiresAt === 'number' && draft.expiresAt > Date.now()) return draft;
  } catch { /* Treat malformed or unavailable storage as no draft. */ }
  clearStaffRoleDraft();
  return null;
}
