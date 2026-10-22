import type { DeviceStatus } from './types';
export type DemoTelemetry = {
  status: DeviceStatus;
  todayHours: number;
  totalHours: number;
  load: number;
  x: number;
  y: number;
  z: number;
  heartbeat: string;
  grab: '闭合' | '打开';
  mode: '自动' | '手动';
  fault: string;
};
const fixture = (
  status: DeviceStatus,
  hours: number,
  load: number,
  x: number,
  y: number,
  z: number,
  mode: '自动' | '手动' = '自动'
): DemoTelemetry => ({
  status,
  todayHours: hours,
  totalHours: 840 + Math.round(x * 23),
  load,
  x,
  y,
  z,
  heartbeat: '2026-10-20 10:00:00',
  grab: status === 'STANDBY' ? '打开' : '闭合',
  mode,
  fault:
    status === 'ALARM'
      ? 'OL-101 · 模拟载荷阈值告警'
      : status === 'MAINTENANCE'
        ? '保养工单处理中（模拟）'
        : '无活动故障',
});
// Real archives remain NOT_COMMISSIONED. These are fixed demonstration telemetry fixtures.
export const realDemoTelemetry: Record<string, DemoTelemetry> = {
  'SAMPLE-001': fixture('RUNNING', 6.8, 62, 32.4, 10.2, 7.8),
  'SAMPLE-002': fixture('STANDBY', 3.2, 0, 18.6, 7.4, 3.2),
  'SAMPLE-003': fixture('RUNNING', 7.1, 71, 46.2, 12.1, 8.4),
  'SAMPLE-004-A': fixture('RUNNING', 5.7, 58, 28.5, 11.7, 6.9),
  'SAMPLE-004-B': fixture('STANDBY', 4.3, 0, 16.4, 4.2, 2.1),
  'SAMPLE-005-A': fixture('ALARM', 4.9, 88, 41.2, 15.3, 9.2, '手动'),
  'SAMPLE-005-B': fixture('RUNNING', 6.5, 54, 26.7, 8.2, 5.6),
  'SAMPLE-006-A': fixture('RUNNING', 7.4, 67, 38.6, 14.7, 8.1),
  'SAMPLE-006-B': fixture('MAINTENANCE', 1.6, 0, 12.2, 3.4, 1.5, '手动'),
  'SAMPLE-006-C': fixture('RUNNING', 6.2, 73, 44.8, 18.3, 10.4),
};
