// R7 F-10: the single front-end copy of the authoritative asset (whole-machine) types. It must
// equal internal/asset AssetTypes (tests/contract/asset-types.test.mjs). Equipment type codes
// (MOTOR, PLC, …) are component types and never appear here.
export const ASSET_TYPES = ['CRANE', 'GANTRY', 'BRIDGE', 'PORTAL', 'JIB'] as const;

/**
 * Options for an edit form: the authoritative list, plus the record's current legacy value (shown
 * as 「旧类型」) so an existing non-standard row is displayed truthfully and can be kept or
 * normalized — never silently rewritten.
 */
export function assetTypeOptions(current?: string | null): { value: string; legacy: boolean }[] {
  const base = ASSET_TYPES.map((value) => ({ value, legacy: false }));
  return current && !(ASSET_TYPES as readonly string[]).includes(current) ? [{ value: current, legacy: true }, ...base] : base;
}
