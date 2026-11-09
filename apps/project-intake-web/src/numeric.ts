import type { KnowledgeState } from './model';

// Numeric fields carry an explicit knowledge flag so that "unknown" is never
// stored as 0 and a migrated legacy 0 is never silently promoted to a fact.
export type Knowledge = Record<string, KnowledgeState>;

/** Parse user text: empty means unknown (null). Returns undefined for invalid text. */
export function parseNumber(raw: string, integer = false): number | null | undefined {
  const text = raw.trim();
  if (text === '') return null;
  const n = Number(text);
  if (!Number.isFinite(n) || n < 0) return undefined;
  if (integer && !Number.isInteger(n)) return undefined;
  return n;
}

/** Apply a user edit: any typed value is KNOWN, a cleared field is UNKNOWN. */
export function edit(knowledge: Knowledge, key: string, value: number | null): Knowledge {
  return { ...knowledge, [key]: value === null ? 'UNKNOWN' : 'KNOWN' };
}

/** Explicitly confirm a legacy zero as a true zero. */
export function confirmZero(knowledge: Knowledge, key: string): Knowledge {
  return { ...knowledge, [key]: 'KNOWN' };
}

/** Normalize before sending: keep every flag consistent with its value. */
export function consistent(knowledge: Knowledge | undefined, values: Record<string, number | null | undefined>): Knowledge {
  const out: Knowledge = {};
  for (const [key, raw] of Object.entries(values)) {
    const value = raw ?? null;
    const state = knowledge?.[key];
    if (value === null) out[key] = 'UNKNOWN';
    else if (state === 'LEGACY_UNCONFIRMED' && value === 0) out[key] = 'LEGACY_UNCONFIRMED';
    else out[key] = 'KNOWN';
  }
  return out;
}

export const isLegacyZero = (knowledge: Knowledge | undefined, key: string, value: number | null | undefined) =>
  knowledge?.[key] === 'LEGACY_UNCONFIRMED' && value === 0;

// G3-3B-R1: money input. The server stores numeric(18,2) and the JSON number;
// both must equal the user's text exactly. Mirrors checkAmountToken() in
// services/project-intake-api/amount_input.go.
export const MONEY_MAX = 100000000000000; // 1e14 yuan, validateInput limit
export type MoneyResult = { value: number | null } | { error: string };

export function parseMoney(raw: string): MoneyResult {
  const text = raw.trim().replace(/,/g, '');
  if (text === '') return { value: null };
  const m = /^(0|[1-9]\d*)(?:\.(\d+))?$/.exec(text);
  if (!m) return { error: '请输入不小于 0 的金额（元），例如 125000.25' };
  const [, int, frac = ''] = m;
  if (frac.replace(/0+$/, '').length > 2) return { error: '金额最多保留两位小数（精确到分）' };
  // Compare as integer cents with BigInt: never trust Number for the range.
  const cents = BigInt(int) * 100n + BigInt((frac + '00').slice(0, 2));
  if (cents > BigInt(MONEY_MAX) * 100n) return { error: '金额超出允许范围（0 至 100,000,000,000,000 元）' };
  const value = Number(text);
  // The JSON number the browser will send must denote exactly the same cents.
  const sent = /^(\d+)(?:\.(\d+))?$/.exec(String(value));
  const sentCents = sent ? BigInt(sent[1]) * 100n + BigInt(((sent[2] || '') + '00').slice(0, 2)) : -1n;
  if (!sent || sentCents !== cents || (sent[2] || '').length > 2) return { error: '金额位数过多，无法精确保存到分；请改为整元或较小的金额' };
  return { value };
}
