// Pure helpers for the homepage region deep link (H1-2 D3). The drill path is the
// chain of administrative adcodes below the country (province → prefecture →
// county); it is resolved from the same geo-manifest the scene uses, so URL, Pinia
// and scene agree on one representation.

export type RegionManifestEntry = { adcode: number; parent: number | null; level: string; name?: string };
export type RegionManifest = Record<string | number, RegionManifestEntry>;

export const COUNTRY_CODE = 100000;

/** `?region=` value → adcode, or null when absent/malformed. */
export function parseRegionParam(value: unknown): number | null {
  if (typeof value !== 'string' || !/^\d{6}$/.test(value)) return null;
  const code = Number(value);
  return code === COUNTRY_CODE ? null : code;
}

/**
 * Full path from the first level below the country down to `code`, e.g.
 * 330481 → [330000, 330400, 330481]. Returns null if the code (or any ancestor)
 * is unknown or the chain does not end at the country — never a partial path.
 */
export function regionPathOf(code: number, manifest: RegionManifest): number[] | null {
  const path: number[] = [];
  const seen = new Set<number>();
  let cur: number | null = code;
  while (cur !== null && cur !== COUNTRY_CODE) {
    if (seen.has(cur)) return null; // cyclic manifest — refuse
    seen.add(cur);
    const entry: RegionManifestEntry | undefined = manifest[cur];
    if (!entry) return null;
    path.unshift(cur);
    cur = entry.parent;
  }
  return cur === COUNTRY_CODE ? path : null;
}

export type RegionTransition = {
  /** Index in the scene stack (0 = country) to go back to first, or null if no back step is needed. */
  backTo: number | null;
  /** Codes to enter, in order, after the optional back step. */
  enter: number[];
};

/**
 * Minimal scene steps to move from the displayed path to the target path: keep
 * the common prefix, go back to it if the current path is deeper or diverges,
 * then enter the remaining codes one level at a time.
 */
export function planRegionTransition(current: readonly number[], target: readonly number[]): RegionTransition {
  let common = 0;
  while (common < current.length && common < target.length && current[common] === target[common]) common++;
  const backTo = common < current.length ? common : null; // stack index: 0 = country, i = current[i-1]
  return { backTo, enter: target.slice(common) };
}

export function samePath(a: readonly number[], b: readonly number[]): boolean {
  return a.length === b.length && a.every((v, i) => v === b[i]);
}

/** The leaf adcode of a path as a URL value, or undefined for the country. */
export function regionParamOf(path: readonly number[]): string | undefined {
  return path.length ? String(path[path.length - 1]) : undefined;
}
