// Project-scope resolution for business ledgers. The top command bar writes
// ?project=; pages must never invent a scope (no silent "first project") and must
// key caches by the effective scope so P001 results are never reused for P002.

export type Grant = { projectId: string; projectName: string };

/** The effective scope from a route query value: a non-empty string, else ''. */
export function scopeFromQuery(value: unknown): string {
  return typeof value === 'string' ? value.trim() : '';
}

/**
 * Whether the scope is one the caller can read. '' (all authorized projects) is
 * always valid; an unknown/unauthorized id is not (the server returns an empty
 * list for it, and the UI should say so rather than show stale data).
 */
export function isAuthorizedScope(scope: string, grants: readonly Grant[]): boolean {
  return scope === '' || grants.some((g) => g.projectId === scope);
}

/** Display name for a project id from the caller's grants; falls back to the id, never invents a name. */
export function projectLabel(projectId: string, grants: readonly Grant[]): string {
  return grants.find((g) => g.projectId === projectId)?.projectName ?? projectId;
}

/** Cache key segment: distinct per scope, with '' normalized to an explicit "all" marker. */
export function scopeKey(scope: string): string {
  return scope === '' ? '__all_authorized__' : scope;
}
