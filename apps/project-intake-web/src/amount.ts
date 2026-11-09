// G3-3B: amounts arrive as fixed-point decimal strings ("125000.25"). They are
// formatted as strings and never converted to a JS number, so large values
// keep every digit.

export interface AmountSummary {
  knownTotal: string;
  knownCount: number;
  knownZeroCount: number;
  knownImplicitCount: number;
  unknownCount: number;
  legacyUnconfirmedCount: number;
  invalidCount: number;
}
export interface StageSummaryResponse {
  asOf: string;
  scope: 'OWN' | 'TEAM' | 'ALL_ASSIGNED' | 'ALL';
  filters: Record<string, string>;
  total: number;
  stages: { code: string; count: number; amount: AmountSummary }[];
  unclassified: number;
  amount: AmountSummary;
  unclassifiedAmount: AmountSummary;
}

const decimal = /^(\d+)\.(\d{2})$/;

/** "1234567.80" → "1,234,567.80". Returns null for anything not a 2-place decimal. */
export function groupDecimal(value: string): string | null {
  const m = decimal.exec(value);
  if (!m) return null;
  const int = m[1].replace(/^0+(?=\d)/, '');
  return int.replace(/\B(?=(\d{3})+(?!\d))/g, ',') + '.' + m[2];
}

/** Display with the unit; an unexpected value is shown as an explicit error, never as 0. */
export function yuan(value: string): string {
  const g = groupDecimal(value);
  return g === null ? '金额格式异常' : '¥' + g;
}

/** Share of a stage in the known total, for a bar width only (two decimals of
 * percent are enough); computed from the integer cents as BigInt. */
export function sharePercent(part: string, whole: string): number {
  const cents = (v: string) => {
    const m = decimal.exec(v);
    return m ? BigInt(m[1] + m[2]) : 0n;
  };
  const w = cents(whole);
  if (w === 0n) return 0;
  return Number((cents(part) * 10000n) / w) / 100;
}
