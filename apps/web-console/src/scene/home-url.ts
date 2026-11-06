// Pure URL ↔ workspace mapping for the homepage (no Vue / router imports, unit
// tested). The store is the single source of truth; the URL is a serialization
// of it. Reading and writing are separate functions so a caller can compare and
// skip no-op writes, which is what prevents URL ↔ Pinia ↔ Scene loops.
import { parseRegionParam, regionParamOf, samePath } from './region-path';
import { parseStatusParam, parseViewParam, type HomeStatusFilter, type HomeView } from './home-metrics';

export type HomeQuery = Record<string, string | undefined>;

export type HomeUrlState = {
  regionPath: readonly number[];
  regionTarget: readonly number[];
  selectedId: string | null;
  selectedProjectId: string | null;
  status: string;
  view: HomeView;
};

/** Keys owned by the homepage; any other query key is preserved untouched. */
export const HOME_KEYS = ['region', 'asset', 'project', 'status', 'view'] as const;

export type ParsedHomeQuery = {
  asset: string | null;
  project: string | null;
  /** Requested region leaf adcode; null when absent. */
  region: number | null;
  /** True when `region` was present but not a 6-digit adcode. */
  regionMalformed: boolean;
  status: HomeStatusFilter | null;
  statusMalformed: boolean;
  view: HomeView;
};

const str = (v: unknown): string | null => (typeof v === 'string' && v.trim() !== '' ? v.trim() : null);

export function parseHomeQuery(query: Record<string, unknown>): ParsedHomeQuery {
  const rawRegion = str(query.region);
  const region = parseRegionParam(rawRegion ?? undefined);
  const rawStatus = str(query.status);
  const status = parseStatusParam(rawStatus ?? undefined);
  return {
    asset: str(query.asset),
    project: str(query.project),
    region,
    regionMalformed: rawRegion !== null && region === null && rawRegion !== '100000',
    status,
    statusMalformed: rawStatus !== null && status === null,
    view: parseViewParam(query.view),
  };
}

export type HomeQueryWrite = { query: HomeQuery; history: 'push' | 'replace' } | null;

/**
 * The query the current state should be serialized to, or null when the URL
 * already matches. Region is written only when the displayed path has reached
 * the requested one (no intermediate levels during a multi-step drill), so a
 * deep link is never truncated by its own convergence. Navigation-like changes
 * (region / selection) push a history entry; filter/view changes replace.
 */
export function homeQueryFor(state: HomeUrlState, current: Record<string, unknown>): HomeQueryWrite {
  const next: HomeQuery = {};
  for (const [k, v] of Object.entries(current)) if (typeof v === 'string') next[k] = v;
  const converged = samePath(state.regionPath, state.regionTarget);
  if (converged) next.region = regionParamOf(state.regionPath);
  next.asset = state.selectedId || undefined;
  next.project = state.selectedId ? undefined : state.selectedProjectId || undefined;
  next.status = state.status !== 'all' ? state.status : undefined;
  next.view = state.view === '2d' ? '2d' : undefined;
  for (const k of Object.keys(next)) if (next[k] === undefined) delete next[k];

  const same = (k: string) => (str(current[k]) ?? undefined) === next[k];
  if (HOME_KEYS.every(same)) return null;
  const navigation = !same('region') || !same('asset') || !same('project');
  return { query: next, history: navigation ? 'push' : 'replace' };
}
