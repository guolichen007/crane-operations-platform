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

export type ScopeState = 'session-loading' | 'unauthorized' | 'ready';

/**
 * Whether a project-scoped list may be requested yet.
 *  - Production auth with no session yet: 'session-loading' — do not request and
 *    do not show an empty state (it would be a false "no data").
 *  - A scope outside the grants: 'unauthorized' — do not request.
 *  - Otherwise 'ready'. Development mode (no OIDC) has no grants to wait for.
 */
export function scopeState(productionAuth: boolean, grants: readonly Grant[] | null, scope: string): ScopeState {
  if (productionAuth && grants === null) return 'session-loading';
  if (productionAuth && !isAuthorizedScope(scope, grants ?? [])) return 'unauthorized';
  return 'ready';
}

/** Top-bar label for a route: only pages that really use ?project= show a project selector. */
export const PROJECT_SCOPED_PATHS = ['/overview', '/projects', '/devices', '/assets', '/locations'];
export function scopeSupported(path: string): boolean {
  return PROJECT_SCOPED_PATHS.includes(path);
}
/**
 * What ?project= means on a page. On the overview it only focuses the map on a
 * project (the scene still contains every authorized project); on the ledgers it
 * filters the data. The top bar labels the selector accordingly.
 */
export type ScopeMode = 'focus' | 'filter' | 'none';
export function scopeMode(path: string): ScopeMode {
  if (!scopeSupported(path)) return 'none';
  return path === '/overview' ? 'focus' : 'filter';
}

// ---- Asset ledger return context ----
export type LedgerQuery = Record<string, string>;
const LEDGER_KEYS = ['project', 'q', 'status', 'type', 'sort', 'page'] as const;
let lastLedgerQuery: LedgerQuery = {};

/** Keep only the known ledger keys with non-empty string values. */
export function ledgerQueryFrom(query: Record<string, unknown>): LedgerQuery {
  const out: LedgerQuery = {};
  for (const k of LEDGER_KEYS) {
    const v = query[k];
    if (typeof v === 'string' && v.trim() !== '') out[k] = v.trim();
  }
  return out;
}
export function rememberLedgerQuery(query: Record<string, unknown>): void {
  lastLedgerQuery = ledgerQueryFrom(query);
}
/**
 * The query to return to the ledger with. The project scope is re-checked
 * against the caller's current grants: an unauthorized (or no longer
 * authorized) project is dropped — and with it the page number, which belonged
 * to that scope — so the return never targets a project the caller cannot read.
 * Before the session is known (grants === null) the scope is dropped as well.
 */
export function ledgerReturnQuery(grants: readonly Grant[] | null, remembered: LedgerQuery = lastLedgerQuery): LedgerQuery {
  const out = { ...remembered };
  if (out.project !== undefined && (grants === null || !isAuthorizedScope(out.project, grants))) {
    delete out.project;
    delete out.page;
  }
  return out;
}
