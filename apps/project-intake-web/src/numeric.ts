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
