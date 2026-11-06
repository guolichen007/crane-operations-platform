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
  const v = (n: number) => (m ? n : null);
  if (mode === 'api')
    return [
      { id: 'devices', label: 'Inventory 设备', value: v(m?.devices ?? 0), unit: '台', detail: m ? `${m.projects} 个项目 · 无空间位置 ${m.unlocated}` : '加载中', filter: 'all', tone: 'neutral', tag: 'INVENTORY' },
      { id: 'real-running', label: '真实运行', value: v(m?.realRunning ?? 0), unit: '台', detail: m ? `REAL 在线 ${m.realOnline} · SIM 绑定 ${m.simulationBound}` : '加载中', filter: 'REAL_RUNNING', tone: 'ok' },
      { id: 'alarm', label: '告警设备', value: v(m?.alarmDevices ?? 0), unit: '台', detail: alarmRecords === null ? '告警记录未加载' : `最近告警记录 ${alarmRecords} 条（非未处理数）`, filter: 'ALARM', tone: 'critical' },
      { id: 'offline', label: '离线 / 状态未知', value: v(m?.offline ?? 0), unit: '台', detail: '有 Runtime Binding 但未连接', filter: 'OFFLINE', tone: 'muted' },
      { id: 'not-commissioned', label: '尚未投运', value: v(m?.notCommissioned ?? 0), unit: '台', detail: '无 Runtime Binding', filter: 'NOT_COMMISSIONED', tone: 'warning' },
    ];
  return [
    { id: 'devices', label: '设备总数', value: v(m?.devices ?? 0), unit: '台', detail: m ? `${m.projects} 个项目（含演示）` : '', filter: 'all', tone: 'neutral', tag: 'DEMO' },
    { id: 'running', label: '演示运行中', value: v(m?.running ?? 0), unit: '台', detail: '模拟运行态，非现场真实', filter: 'RUNNING', tone: 'ok', tag: 'DEMO' },
    { id: 'alarm', label: '告警设备', value: v(m?.alarmDevices ?? 0), unit: '台', detail: '固定模拟告警', filter: 'ALARM', tone: 'critical', tag: 'DEMO' },
    { id: 'offline', label: '离线', value: v(m?.offline ?? 0), unit: '台', detail: `维护中 ${m?.maintenance ?? 0} 台`, filter: 'OFFLINE', tone: 'muted', tag: 'DEMO' },
    { id: 'planned', label: '登记行车', value: v(plannedDevices), unit: '台', detail: '匿名登记项目 · 全部尚未投运', filter: 'planned', tone: 'warning' },
  ];
}
