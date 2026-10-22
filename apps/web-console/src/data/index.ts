import { realDemoTelemetry } from './real-telemetry';
import { realProjects } from './real-projects';
import { demoProjects } from './demo-projects';
import type { Device, DeviceStatus, Project, SourceFilter } from './types';
export { realProjects, demoProjects };
export const projects = [...realProjects, ...demoProjects];
const demoStatuses: DeviceStatus[] = [
  ...Array<DeviceStatus>(26).fill('RUNNING'),
  ...Array<DeviceStatus>(9).fill('STANDBY'),
  ...Array<DeviceStatus>(4).fill('ALARM'),
  ...Array<DeviceStatus>(2).fill('OFFLINE'),
  ...Array<DeviceStatus>(3).fill('MAINTENANCE'),
];
export function expandProjects(items: Project[]): Device[] {
  return items.flatMap((p) =>
    Array.from({ length: p.quantity }, (_, i) => {
      const n =
        p.dataSource === 'real'
          ? 0
          : 10 + demoProjects.findIndex((project) => project.id === p.id) * 2 + i;
      const deviceId = p.quantity > 1 ? `${p.id}-${String.fromCharCode(65 + i)}` : p.id;
      return {
        ...p,
        deviceId,
        telemetrySource: 'demo' as const,
        commissioningStatus:
          p.dataSource === 'real' ? ('NOT_COMMISSIONED' as const) : ('OPERATIONAL' as const),
        grab: '闭合' as const,
        mode: '自动' as const,
        fault: '无活动故障',
        deviceName: `${p.projectName} · ${String.fromCharCode(65 + i)}机`,
        status: p.dataSource === 'real' ? 'NOT_COMMISSIONED' : demoStatuses[(n - 10 + 44) % 44]!,
        todayHours: p.dataSource === 'real' ? 0 : 6 + (n % 9) * 0.7,
        totalHours: p.dataSource === 'real' ? 0 : 1286 + n * 137,
        load: 34 + (n % 56),
        x: 18 + (n % 42),
        y: 4 + (n % 18),
        z: 2 + (n % 12),
        heartbeat: '2026-10-20 10:00:00',
        ...(p.dataSource === 'real' ? realDemoTelemetry[deviceId] : {}),
      };
    })
  );
}
export const devices = expandProjects(projects);
export function filterProjects(source: SourceFilter, items = projects) {
  return items.filter((p) => source === 'all' || p.dataSource === source);
}
export function aggregate(items: Project[]) {
  return {
    projects: items.length,
    cranes: items.reduce((s, p) => s + p.quantity, 0),
    provinces: new Set(items.map((p) => p.province)).size,
  };
}
export const alarms = devices
  .filter(
    (d) => d.telemetrySource === 'demo' && ['ALARM', 'OFFLINE', 'MAINTENANCE'].includes(d.status)
  )
  .map((d, i) => ({
    id: `ALM-20261019-${i + 1}`,
    device: d,
    level: ['一级', '二级', '提示'][i % 3]!,
    code: ['OL-101', 'TR-203', 'NET-001'][i % 3]!,
    description: ['起升载荷接近安全阈值', '大车行走变频器温度偏高', '心跳连接中断，请检查链路'][
      i % 3
    ]!,
    time: `2026-10-19 ${String(14 - Math.floor(i / 2)).padStart(2, '0')}:${String(32 - i * 3).padStart(2, '0')}:08`,
  }));
export const events = [
  { time: '14:32:08', kind: '恢复', text: '南京 · 循环资源 / A机 状态恢复', color: '#55d6a4' },
  { time: '14:30:42', kind: '告警', text: '包头 · 港口物流 / A机 载荷阈值告警', color: '#ff665f' },
  { time: '14:28:16', kind: '上线', text: '武汉 · 港口物流 / B机 已接入', color: '#51d6e8' },
  { time: '14:21:03', kind: '里程碑', text: 'Project-A / 11 月实施计划已建立', color: '#e8b75d' },
];
