// A pending sensitive staff action survives the enterprise reauthentication round
// trip (sessionStorage, same tab only, five minutes). The server re-checks
// everything on submit; this only restores the administrator's form.
export type StaffDraftAction = 'invite' | 'edit' | 'promote' | 'demote' | 'verify' | 'reactivate';

export interface StaffDraftForm {
  name: string;
  phone: string;
  email: string;
  teamId: string;
  role: string;
  note: string;
  reason: string;
  permissions: string[];
}

export interface StaffDraft {
  actor: string;
  staffId: string; // '' for a new invitation
  action: StaffDraftAction;
  version: number; // 0 for a new invitation
  form: StaffDraftForm;
  expiresAt: number;
}

const key = 'baobei.staffReauthDraft';
const actions: StaffDraftAction[] = ['invite', 'edit', 'promote', 'demote', 'verify', 'reactivate'];
const uuid = /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i;

export function saveStaffDraft(draft: StaffDraft) {
  sessionStorage.setItem(key, JSON.stringify(draft));
}

export function clearStaffDraft() {
  try { sessionStorage.removeItem(key); } catch { /* Storage is optional. */ }
}

function text(value: unknown, max: number) {
  return typeof value === 'string' && value.length <= max;
}

export function readStaffDraft(now = Date.now()): StaffDraft | null {
  try {
    const raw = sessionStorage.getItem(key);
    if (!raw) return null;
    const d = JSON.parse(raw) as StaffDraft;
    const f = d?.form;
    if (typeof d.actor === 'string' && actions.includes(d.action) &&
        (d.action === 'invite' ? d.staffId === '' && d.version === 0 : uuid.test(d.staffId) && Number.isSafeInteger(d.version) && d.version > 0) &&
        f && text(f.name, 128) && text(f.phone, 32) && text(f.email, 254) && text(f.teamId, 36) && text(f.role, 32) &&
        text(f.note, 1000) && text(f.reason, 600) && Array.isArray(f.permissions) && f.permissions.every((p) => text(p, 32)) &&
        typeof d.expiresAt === 'number' && d.expiresAt > now && d.expiresAt <= now + 300_000) return d;
  } catch { /* Treat malformed or unavailable storage as no draft. */ }
  clearStaffDraft();
  return null;
}

export function draftPath(draft: StaffDraft) {
  return draft.staffId ? '/team/members/' + draft.staffId : '/settings/users';
}
