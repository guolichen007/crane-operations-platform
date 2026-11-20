// R9-UX visible-text guard. NOT a naive "any Latin letter fails" check: asset / device / work-order
// numbers, PLC addresses, brand-models, standard numbers and common technical abbreviations are
// allowed. What it rejects (each with a counterexample test in r9-ux-localization.spec.ts):
//   1. raw enum leaks       — UPPER_SNAKE tokens that are platform enums (DRAFT_UNAPPROVED, PCT_100 …)
//   2. English formulas     — the import's English calculation notes (edge-counted, ON duration …)
//   3. bilingual decoration — an all-caps English eyebrow / title line (EQUIPMENT CATALOG …)
//   4. untranslated errors  — server / browser diagnostics as the visible message
import { expect, type Locator, type Page } from '@playwright/test';

/** Platform enum values that must never be the visible text (DB / API values, kept in data-*). */
const ENUMS = [
  'DRAFT_UNAPPROVED', 'APPROVED', 'PENDING_REVIEW', 'UNIT_CONFLICT_PENDING', 'INSUFFICIENT_DATA', 'DUE_SOON', 'OVERDUE', 'WORK_ORDER',
  'PCT_80', 'PCT_90', 'PCT_100', 'LEAD_90', 'LEAD_30', 'LEAD_7', 'TIME_BASED', 'METER_BASED', 'HYBRID', 'REPLACEMENT_DUE', 'CALIBRATION_DUE',
  'STATUTORY_INSPECTION', 'EDGE_COUNT', 'ON_DURATION', 'DAILY_VALUE', 'UNCONFIRMED', 'UNVERIFIED', 'CONFIRMED', 'CUMULATIVE', 'DAILY',
  'IN_PROGRESS', 'ASSIGNED', 'COMPLETED', 'CANCELLED', 'OPEN', 'RESOLVED', 'ACKNOWLEDGED', 'OUT_OF_SERVICE', 'RETIRED', 'DISABLED',
  'ACTIVE', 'DRAFT', 'PLANNED', 'PRE_DEVELOPMENT', 'OPERATIONAL', 'GOOD', 'BAD', 'UNCERTAIN', 'MISSING', 'DECREASE_PENDING', 'TRUSTED',
  'CONTACTOR', 'LIMIT_SWITCH', 'VFD', 'BRAKE', 'WIRE_ROPE', 'MISSING_BIT_INDEX', 'DUPLICATE_PLC_ADDRESS_GROUP', 'UNIT_CONFLICT_HOURS_VS_TONNES',
  'REPLACEMENT', 'INSPECTION', 'CALIBRATION', 'LEDGER', 'SIMULATION', 'TEST', 'REAL', 'BIT', 'WORD', 'UNPARSED', 'NONE',
  'ONLINE', 'OFFLINE', 'STALE', 'STOPPED', 'LIVE', 'IDLE', 'RUNNING', 'FAULT', 'SITE', 'PLANT', 'WORKSHOP', 'BAY', 'RUNWAY',
  'CRANE', 'GANTRY', 'BRIDGE', 'PORTAL', 'JIB', 'LOCAL_SITE', 'INT', 'FLOAT', 'SUCCESS', 'FAILURE',
  'viewer', 'operator', 'maintainer', 'platform_admin', 'system_admin', 'account_manager', 'portfolio_planner', 'portfolio_manager',
  'count', 'success', 'pending', 'conflict', 'identity_provider_failed',
];
const ENUM_SET = new Set(ENUMS);
const ENGLISH_FORMULA = /\b(edge[- ]counted|rising edges?|falling edges?|ON duration|daily tonnes|accumulat(ed|ion) of|delta of|sum of|count of|proxy (of|for)|per day|formula)\b/i;
const UNTRANSLATED_ERROR = /\b(Forbidden|Unauthorized|Not Found|Bad Request|Internal Server Error|Service Unavailable|Failed to fetch|NetworkError|is required|must be|invalid (body|input|value)|stale|version conflict|TypeError|undefined|null|NaN)\b|\[object Object\]/;
/** A line that is only an English heading (two+ capitalised words, no CJK): the old eyebrow pattern. */
const BILINGUAL = /^[A-Z]{2,}(?: [A-Z&·/]{2,})+$/;
/** Allowed all-caps lines (brand, abbreviations). */
const ALLOWED_LINE = /^(U·DAO|U-DAO|PLC|RFID|MQTT|GCJ02|WGS84|API|HTTP|SIM|ID|OK|3D|2D|KPI)( .*)?$/;
/** Tokens allowed although they look like codes: asset / device / work-order / point numbers etc. */
const CODE_LIKE = /^(?:[A-Z]{2,}[-.][A-Z0-9.\-]+|DB\d+[.\w]*|[A-Z]+\d+[A-Z\d\-]*|\d+[A-Za-z]*)$/;

export type Leak = { kind: 'RAW_ENUM' | 'ENGLISH_FORMULA' | 'BILINGUAL' | 'UNTRANSLATED_ERROR'; text: string };

/** Pure detector over visible text lines (unit-tested by counterexamples). */
export function findLeaks(lines: readonly string[]): Leak[] {
  const out: Leak[] = [];
  for (const raw of lines) {
    // A code deliberately shown as technical detail after its Chinese name — 「整数（INT）」、
    // 「旧类型（编码 X，保持不变）」、「编码 X」 — is allowed; the Chinese name is the headline.
    const line = raw.replace(/\s+/g, ' ').trim()
      .replace(/([一-鿿])（(?:编码|代码|技术编码)?\s*[A-Za-z0-9_.\-]+(?:[，,][^）]*)?）/g, '$1')
      .replace(/(?:编码|代码)[：: ]\s*[A-Za-z0-9_.\-]+/g, '');
    if (!line) continue;
    if (ENGLISH_FORMULA.test(line)) out.push({ kind: 'ENGLISH_FORMULA', text: line });
    if (UNTRANSLATED_ERROR.test(line)) out.push({ kind: 'UNTRANSLATED_ERROR', text: line });
    if (BILINGUAL.test(line) && !ALLOWED_LINE.test(line) && !/[一-鿿]/.test(line)) out.push({ kind: 'BILINGUAL', text: line });
    else if (/^[A-Z][A-Z &]{4,} ?\/ ?[一-鿿]/.test(line) || /[一-鿿] ?\/ ?[A-Z][A-Za-z]{3,}( [A-Za-z]+)*$/.test(line)) out.push({ kind: 'BILINGUAL', text: line });
    for (const tok of line.split(/[\s，。、：:；;（）()「」【】·|,/]+/)) {
      if (!tok || CODE_LIKE.test(tok) && !ENUM_SET.has(tok)) continue;
      if (ENUM_SET.has(tok)) out.push({ kind: 'RAW_ENUM', text: `${tok} ← ${line.slice(0, 80)}` });
    }
  }
  return out;
}

/** Visible text lines of a scope: each element's own text nodes, plus placeholder / title / aria-label of visible controls. */
export async function visibleLines(scope: Page | Locator): Promise<string[]> {
  const root = 'locator' in scope && typeof (scope as Page).goto === 'function' ? (scope as Page).locator('body') : scope as Locator;
  return root.evaluate((el) => {
    const out: string[] = [];
    const visible = (e: Element) => { const s = getComputedStyle(e); const r = e.getBoundingClientRect(); return s.visibility !== 'hidden' && s.display !== 'none' && (r.width > 0 || r.height > 0); };
    const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    for (let n = walker.nextNode(); n; n = walker.nextNode()) {
      const p = n.parentElement;
      if (!p || ['SCRIPT', 'STYLE', 'NOSCRIPT'].includes(p.tagName) || !visible(p)) continue;
      const t = (n.textContent ?? '').trim();
      if (t) out.push(t);
    }
    for (const c of el.querySelectorAll<HTMLElement>('[placeholder],[title],[aria-label],option')) {
      if (c.tagName !== 'OPTION' && !visible(c)) continue;
      for (const a of ['placeholder', 'title', 'aria-label']) { const v = c.getAttribute(a); if (v) out.push(v); }
      if (c.tagName === 'OPTION') out.push((c.textContent ?? '').trim());
    }
    return out;
  });
}

export async function expectChineseUI(scope: Page | Locator, where: string): Promise<void> {
  const leaks = findLeaks(await visibleLines(scope));
  expect(leaks, `${where} 可见文本中文化：${JSON.stringify(leaks.slice(0, 12))}`).toEqual([]);
}
