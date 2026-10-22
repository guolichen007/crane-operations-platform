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
};
export type Device = Project & {
  telemetrySource: 'demo';
  commissioningStatus: 'NOT_COMMISSIONED' | 'OPERATIONAL';
  grab: '闭合' | '打开';
  mode: '自动' | '手动';
  fault: string;
  deviceId: string;
  deviceName: string;
  status: DeviceStatus;
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
