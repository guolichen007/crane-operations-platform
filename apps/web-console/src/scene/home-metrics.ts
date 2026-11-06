// Homepage metrics: one place for every number the Operations Center shows, so the
// KPI strip, the Attention list and the Inspector never recompute the same thing
// differently. Pure functions over the provider's view-models; definitions follow
// the frozen status rules in scene/device-status.ts. H0-D1 (empty province) and
// H0-D2 (UTC event time) are H2 items and intentionally not changed here.
import type { Device, Project } from '../data/types';

/** n / d, or null when the denominator is not a positive finite number (never NaN/Infinity). */
export function safeRatio(n: number, d: number): number | null {
  if (!Number.isFinite(n) || !Number.isFinite(d) || d <= 0) return null;
  return n / d;
}

export type HomeMetrics = {
  projects: number;
  devices: number;
  /** Devices whose project has no spatial association (no adminCode / adminPath / coordinates). */
  unlocated: number;
  running: number;
  realRunning: number;
  realOnline: number;
  simulationBound: number;
  testBound: number;
  alarmDevices: number;
  offline: number;
  standby: number;
  notCommissioned: number;
  maintenance: number;
};

export function hasLocation(p: Project): boolean {
  return p.adminPath.length > 0 || p.approximateLng !== 0 || p.approximateLat !== 0;
}

export function homeMetrics(projects: readonly Project[], devices: readonly Device[]): HomeMetrics {
  const count = (pred: (d: Device) => boolean) => devices.reduce((n, d) => n + (pred(d) ? 1 : 0), 0);
  return {
    projects: projects.length,
    devices: devices.length,
    unlocated: count((d) => !hasLocation(d)),
    running: count((d) => d.status === 'RUNNING'),
    realRunning: count((d) => d.runtimeSource === 'REAL' && d.status === 'RUNNING'),
    realOnline: count((d) => d.runtimeSource === 'REAL' && d.status !== 'OFFLINE' && d.status !== 'NOT_COMMISSIONED'),
    simulationBound: count((d) => d.runtimeSource === 'SIMULATION'),
    testBound: count((d) => d.runtimeSource === 'TEST'),
    alarmDevices: count((d) => d.status === 'ALARM'),
    offline: count((d) => d.status === 'OFFLINE'),
    standby: count((d) => d.status === 'STANDBY'),
    notCommissioned: count((d) => d.status === 'NOT_COMMISSIONED'),
    maintenance: count((d) => d.status === 'MAINTENANCE'),
  };
}

/** Homepage status filters that may appear in `?status=`. */
export const HOME_STATUS_FILTERS = ['ALARM', 'OFFLINE', 'NOT_COMMISSIONED', 'RUNNING', 'REAL_RUNNING', 'STANDBY', 'MAINTENANCE', 'UNLOCATED', 'planned'] as const;
export type HomeStatusFilter = (typeof HOME_STATUS_FILTERS)[number];
export function parseStatusParam(value: unknown): HomeStatusFilter | null {
  return typeof value === 'string' && (HOME_STATUS_FILTERS as readonly string[]).includes(value)
    ? (value as HomeStatusFilter)
    : null;
}

/**
 * Data state of the homepage numbers (H1-R1-1). A number may only be shown when
 * it comes from a successful load; a failed first load is "unavailable", never 0.
 *   loading      — no successful load yet, request in flight      → placeholder
 *   unavailable  — no successful load yet, last request failed      → "不可用"
 *   ready        — last request succeeded                           → numbers
 *   refreshing   — numbers from an earlier success, new request in flight
 *   stale        — numbers from an earlier success, the latest request failed → numbers marked 过期
 * An empty successful response is "ready" with zeros — the only case a 0 is shown.
 */
export type HomeDataState = 'loading' | 'unavailable' | 'ready' | 'refreshing' | 'stale';
export function homeDataState(p: { mode: 'api' | 'demo'; loading: boolean; error: string; fetchedAt: string }): HomeDataState {
  if (p.mode === 'demo') return 'ready';
  const has = p.fetchedAt !== '';
  if (!has) return p.loading ? 'loading' : p.error ? 'unavailable' : 'loading';
  if (p.loading) return 'refreshing';
  return p.error ? 'stale' : 'ready';
}
export const hasNumbers = (s: HomeDataState) => s === 'ready' || s === 'refreshing' || s === 'stale';

/**
 * Statistics scope contract (H1-R1-2, see docs/04-前端与可视化/Homepage-H1-R1-统计范围契约.md):
 *   KPI / Attention base set  = authorized ∩ source ∩ displayed region
 *   status filter             → only which devices are displayed (map, Inspector list); never the KPI base
 *   focused project / device  → only the Inspector detail; never the KPI base
 */
export type HomeScope = { source: 'all' | 'real' | 'demo'; regionPath: readonly number[] };
export function inRegion(adminPath: readonly number[], regionPath: readonly number[]): boolean {
  return regionPath.every((c, i) => adminPath[i] === c);
}
export function scopeProjects<T extends Project>(items: readonly T[], s: HomeScope): T[] {
  return items.filter((p) => (s.source === 'all' || p.dataSource === s.source) && inRegion(p.adminPath, s.regionPath));
}
/** Human-readable scope label shown next to the KPI strip (same words as the filters). */
export function scopeLabel(mode: 'api' | 'demo', s: HomeScope, regionName: string): string {
  const source =
    mode === 'api' ? '全部授权项目' : s.source === 'real' ? '匿名登记项目' : s.source === 'demo' ? '演示设备' : '全部展示（含演示）';
  return `${source} · ${s.regionPath.length ? regionName : '全国'}`;
}

export type HomeView = '2d' | '3d';
/** `?view=` → view; anything else is the default (3D, see H1-0 design §7). */
export function parseViewParam(value: unknown): HomeView {
  return value === '2d' ? '2d' : '3d';
}

export type HomeKpi = {
  id: string;
  label: string;
  value: number | null;
  unit: string;
  detail: string;
  /** Status filter the card applies when clicked ('all' clears). */
  filter: HomeStatusFilter | 'all';
  tone: 'neutral' | 'ok' | 'critical' | 'warning' | 'muted';
  tag?: string;
  /** Tooltip explaining a scope exception (e.g. alarm records are not region-scoped). */
  hint?: string;
};

/**
 * The (at most five) homepage KPI cards. Every number comes from homeMetrics();
 * API and demo differ only in labels and source tags — demo numbers are always
 * tagged DEMO, API alarm records are never called "unhandled".
 */
export function homeKpis(
  mode: 'api' | 'demo',
  m: HomeMetrics | null,
  alarmRecords: number | null,
  plannedDevices: number,
): HomeKpi[] {
  // m === null means "no successful data" (loading / unavailable): every value is
  // null and rendered as a placeholder — never a fabricated 0.
  const v = (n: number) => (m ? n : null);
  if (mode === 'api')
    return [
      // The Inventory card opens the Inventory device list for the same scope (a
      // workflow, not a status filter — see the scope contract).
      { id: 'devices', label: 'Inventory 设备', value: v(m?.devices ?? 0), unit: '台', detail: m ? `${m.projects} 个项目 · 无空间位置 ${m.unlocated}` : '—', hint: '点击打开同范围的 Inventory 设备清单', filter: 'all', tone: 'neutral', tag: 'INVENTORY' },
      { id: 'real-running', label: '真实运行', value: v(m?.realRunning ?? 0), unit: '台', detail: m ? `REAL 在线 ${m.realOnline} · SIM 绑定 ${m.simulationBound}` : '—', filter: 'REAL_RUNNING', tone: 'ok' },
      { id: 'alarm', label: '告警设备', value: v(m?.alarmDevices ?? 0), unit: '台', detail: !m || alarmRecords === null ? '告警记录未加载' : `最近告警记录 ${alarmRecords} 条（非未处理数）`, hint: '告警记录为全部授权范围最近 ≤100 条，不随来源/行政区变化；卡片数字为当前范围内的告警设备', filter: 'ALARM', tone: 'critical' },
      { id: 'offline', label: '离线 / 状态未知', value: v(m?.offline ?? 0), unit: '台', detail: '有 Runtime Binding 但未连接', filter: 'OFFLINE', tone: 'muted' },
      { id: 'not-commissioned', label: '尚未投运', value: v(m?.notCommissioned ?? 0), unit: '台', detail: '无 Runtime Binding', filter: 'NOT_COMMISSIONED', tone: 'warning' },
    ];
  return [
    { id: 'devices', label: '设备总数', value: v(m?.devices ?? 0), unit: '台', detail: m ? `${m.projects} 个项目` : '', filter: 'all', tone: 'neutral', tag: 'DEMO' },
    { id: 'running', label: '演示运行中', value: v(m?.running ?? 0), unit: '台', detail: '模拟运行态，非现场真实', filter: 'RUNNING', tone: 'ok', tag: 'DEMO' },
    { id: 'alarm', label: '告警设备', value: v(m?.alarmDevices ?? 0), unit: '台', detail: '固定模拟告警', filter: 'ALARM', tone: 'critical', tag: 'DEMO' },
    { id: 'offline', label: '离线', value: v(m?.offline ?? 0), unit: '台', detail: `维护中 ${m?.maintenance ?? 0} 台`, filter: 'OFFLINE', tone: 'muted', tag: 'DEMO' },
    { id: 'planned', label: '登记行车', value: v(plannedDevices), unit: '台', detail: '匿名登记项目 · 全部尚未投运', filter: 'planned', tone: 'warning' },
  ];
}
