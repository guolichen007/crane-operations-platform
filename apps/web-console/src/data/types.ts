export type DeviceStatus =
  'RUNNING' | 'STANDBY' | 'ALARM' | 'OFFLINE' | 'MAINTENANCE' | 'NOT_COMMISSIONED';
export type Lifecycle = 'PLANNED' | 'PRE_DEVELOPMENT' | 'OPERATIONAL';
export type SourceFilter = 'all' | 'real' | 'demo';
export type Project = {
  id: string;
  projectName: string;
  dataSource: 'real' | 'demo';
  province: string;
  city: string | null;
  county: string | null;
  locationLabel: string;
  approximateLng: number;
  approximateLat: number;
  craneModel: string;
  ratedCapacityT: number;
  spanM: number;
  workClass: string;
  grabType: string;
  quantity: number;
  lifecycle: Lifecycle;
  plannedImplementation: string;
  scheduleText: string;
  coordinateAccuracy: 'city_center_demo' | 'county_center_demo';
  milestones?: { date: string; name: string }[];
  adminPath: number[];
  /** API projects carry an admin code instead of exact coordinates. */
  adminCode?: number;
};
export type TelemetrySource = 'demo' | 'cloud' | 'none';
/** Site source: REAL=真实现场, SIMULATION=模拟, TEST=测试, null=无运行身份. */
export type RuntimeSource = 'REAL' | 'SIMULATION' | 'TEST' | null;
export type Device = Project & {
  telemetrySource: TelemetrySource;
  /** 现场来源独立于遥测来源：SIMULATION 即使有 Shadow 也绝不当真实现场。 */
  runtimeSource: RuntimeSource;
  commissioningStatus: 'NOT_COMMISSIONED' | 'OPERATIONAL';
  /** null = 未知（无遥测时禁止伪造“闭合/打开”）。 */
  grab: '闭合' | '打开' | null;
  /** null = 未知（无遥测时禁止伪造“自动/手动”）。 */
  mode: '自动' | '手动' | null;
  fault: string;
  deviceId: string;
  deviceName: string;
  status: DeviceStatus;
  /** NaN = 未知，禁止用 0 伪造。 */
  todayHours: number;
  totalHours: number;
  load: number;
  x: number;
  y: number;
  z: number;
  heartbeat: string;
};
export const statusText: Record<DeviceStatus, string> = {
  RUNNING: '运行中',
  STANDBY: '待机',
  ALARM: '告警',
  OFFLINE: '离线',
  MAINTENANCE: '维护中',
  NOT_COMMISSIONED: '尚未投运',
};
export const lifecycleText: Record<Lifecycle, string> = {
  PLANNED: '计划实施',
  PRE_DEVELOPMENT: '软件预开发',
  OPERATIONAL: '已投运',
};
export const statusColor: Record<DeviceStatus, string> = {
  RUNNING: '#55d6a4',
  STANDBY: '#6ecdf2',
  ALARM: '#ff665f',
  OFFLINE: '#66727e',
  MAINTENANCE: '#e8b75d',
  NOT_COMMISSIONED: '#e8b75d',
};
