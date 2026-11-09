// Browser-test mock of GET /api/v1/operations/summary derived from an Inventory fixture (H2-1D).
// Counts follow the server's definitions closely enough for UI tests: registered = assets of projects
// inside the region; regions[] = direct children (adcode prefix: province 2 digits, prefecture 4);
// unlocated only at country scope. Runtime counts default to 0 unless overridden. Test data only.
import type { Page, Route } from '@playwright/test';
import { summary, type SummaryOverrides } from '../../apps/web-console/tests/summary-fixtures';
import type { OperationsSummary } from '../../contracts/typescript/types.gen';

export type InvProject = { id: string; adminCode: number | null };
export type InvAsset = { projectId: string };
export type SummaryCall = { region: number | null; window: string; n: number };
export type SummaryReply = OperationsSummary | 'fail' | 'hang' | { status: number } | { delayMs: number; body: OperationsSummary };

const NAMES: Record<number, string> = { 330000: '浙江省', 330400: '嘉兴市', 330481: '海宁市', 440000: '广东省', 440300: '深圳市', 110000: '北京市' };

function contains(scope: number, code: number): boolean {
  if (scope === 100000) return true;
  if (scope % 10000 === 0) return Math.floor(code / 10000) === scope / 10000;
  if (scope % 100 === 0) return Math.floor(code / 100) === scope / 100;
  return code === scope;
}
function child(scope: number, code: number): number | 0 {
  if (code === scope) return 0;
  if (scope === 100000) return Math.floor(code / 10000) * 10000;
  if (scope % 10000 === 0) return Math.floor(code / 100) * 100 === code ? code : Math.floor(code / 100) * 100;
  if (scope % 100 === 0) return code;
  return 0;
}

export function inventorySummary(projects: InvProject[], assets: InvAsset[], region: number | null, o: SummaryOverrides = {}): OperationsSummary {
  const scope = region ?? 100000;
  const inScope = projects.filter((p) => (p.adminCode === null ? scope === 100000 : contains(scope, p.adminCode)));
  const ids = new Set(inScope.map((p) => p.id));
  const count = (pred: (p: InvProject) => boolean) => assets.filter((a) => ids.has(a.projectId) && pred(projects.find((p) => p.id === a.projectId)!)).length;
  const kids = new Map<number, number>();
  let atScope = 0;
  for (const p of inScope) {
    if (p.adminCode === null) continue;
    const c = child(scope, p.adminCode);
    const n = count((x) => x.id === p.id);
    if (c === 0) atScope += n;
    else kids.set(c, (kids.get(c) ?? 0) + n);
  }
  return summary({
    region,
    regionName: region ? NAMES[region] ?? String(region) : '全部授权范围',
    authorizedProjects: projects.length,
    projects: inScope.length,
    registered: count(() => true),
    unlocated: scope === 100000 ? count((p) => p.adminCode === null) : 0,
    atScopeLevel: atScope,
    noRuntime: count(() => true),
    regions: [...kids].map(([adcode, registered]) => ({ adcode, name: NAMES[adcode] ?? String(adcode), registered, realConnected: 0, realAlarmDevices: 0 })),
    ...o,
  });
}

/**
 * Routes the summary endpoint. `reply` decides per call (n = 1-based call number). Returns the
 * recorded calls. Must be registered AFTER a catch-all `**\/api/v1/**` route (Playwright matches the
 * most recently registered route first).
 */
export async function routeSummary(page: Page, reply: (c: SummaryCall) => SummaryReply | Promise<SummaryReply>): Promise<SummaryCall[]> {
  const calls: SummaryCall[] = [];
  await page.route('**/api/v1/operations/summary*', async (r: Route) => {
    const u = new URL(r.request().url());
    const region = u.searchParams.get('region');
    const call = { region: region ? Number(region) : null, window: u.searchParams.get('alarmWindow') ?? 'PT24H', n: calls.length + 1 };
    calls.push(call);
    const out = await reply(call);
    if (out === 'hang') return; // never answered (the client times out / aborts)
    if (out === 'fail') return r.fulfill({ status: 503, json: { error: 'summary_unavailable' } }).catch(() => undefined);
    if ('status' in out && !('schemaVersion' in out)) return r.fulfill({ status: out.status, json: { error: 'x' } }).catch(() => undefined);
    if ('delayMs' in out) {
      await new Promise((res) => setTimeout(res, out.delayMs));
      return r.fulfill({ json: out.body }).catch(() => undefined);
    }
    return r.fulfill({ json: out }).catch(() => undefined);
  });
  return calls;
}
