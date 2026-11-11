// Client-side mirror of the server staff profile rules (staff.go). The server
// remains authoritative; this only gives immediate, field-level guidance.
export interface StaffFormValues {
  name: string;
  phone: string;
  email: string;
  role: string;
  teamId: string;
  reason: string;
}
export type StaffField = 'name' | 'phone' | 'email' | 'teamId' | 'reason';
export type StaffErrors = Partial<Record<StaffField, string>>;

export const fieldOrder: StaffField[] = ['name', 'phone', 'email', 'teamId', 'reason'];
export const fieldLabels: Record<StaffField, string> = {
  name: '姓名', phone: '工作手机', email: '工作邮箱', teamId: '团队', reason: '操作原因',
};

export function normalizePhone(raw: string): string | null {
  let v = raw.trim().replace(/[\s\-() ]/g, '');
  if (v.startsWith('0086')) v = '+86' + v.slice(4);
  else if (v.length === 13 && v.startsWith('86')) v = '+' + v;
  else if (v.length === 11 && v.startsWith('1')) v = '+86' + v;
  if (!/^\+[1-9]\d{7,14}$/.test(v)) return null;
  if (v.startsWith('+86') && !/^\+861[3-9]\d{9}$/.test(v)) return null;
  return v;
}

export function validEmail(raw: string) {
  const v = raw.trim().toLowerCase();
  return v.length <= 254 && /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v);
}

export interface StaffRules {
  phoneRequired: boolean; // false only when editing a legacy member without a phone
  reasonRequired: boolean; // role/team/state changes and sensitive operations
}

export function validateStaff(v: StaffFormValues, rules: StaffRules): StaffErrors {
  const e: StaffErrors = {};
  const name = v.name.trim();
  if (!name) e.name = '请填写姓名';
  else if (/^[\d\s]+$/.test(name)) e.name = '姓名不能只包含数字';
  else if ([...name].length > 128) e.name = '姓名不能超过 128 个字';
  if (!v.phone.trim()) {
    if (rules.phoneRequired) e.phone = '请填写工作手机';
  } else if (!normalizePhone(v.phone)) {
    e.phone = '手机格式不正确：国内手机填 11 位号码，境外号码以 + 和国家码开头';
  }
  if (v.email.trim() && !validEmail(v.email)) e.email = '邮箱格式不正确；没有邮箱可以留空';
  if ((v.role === 'baobei_sales' || v.role === 'baobei_manager') && !v.teamId) e.teamId = '销售和主管必须选择所属团队';
  if (rules.reasonRequired && !v.reason.trim()) e.reason = '请填写操作原因';
  else if (v.reason.length > 600) e.reason = '操作原因不能超过 600 字';
  return e;
}

// Server field errors map back to the same inputs.
export const serverFieldErrors: Record<string, StaffField> = {
  STAFF_NAME_INVALID: 'name',
  STAFF_PHONE_REQUIRED: 'phone',
  STAFF_PHONE_INVALID: 'phone',
  STAFF_PHONE_DUPLICATE: 'phone',
  STAFF_EMAIL_INVALID: 'email',
  STAFF_EMAIL_DUPLICATE: 'email',
  STAFF_TEAM_REQUIRED: 'teamId',
  STAFF_REASON_REQUIRED: 'reason',
};
