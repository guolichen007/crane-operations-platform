import type { Device, Project } from '../../data/types';

export function provinceClusters(projects: Project[]) {
  const clusters = new Map<number, Project[]>();
  for (const p of projects) {
    const code = p.adminPath[0]!;
    clusters.set(code, [...(clusters.get(code) ?? []), p]);
  }
  return clusters;
}

export function provinceDeviceCounts(devices: Device[]) {
  const counts = new Map<number, number>();
  for (const device of devices) {
    const code = device.adminPath[0]!;
    counts.set(code, (counts.get(code) ?? 0) + 1);
  }
  return counts;
}

export type FleetStatus = 'RUNNING' | 'STANDBY' | 'ALARM' | 'OFFLINE' | 'MAINTENANCE' | 'PLANNED';

export type ProvinceFleet = {
  adcode: number;
  total: number;
  running: number;
  standby: number;
  alarm: number;
  offline: number;
  maintenance: number;
  planned: number;
  projects: number;
  types: Record<string, number>;
};

const RUNTIME_STATUSES: FleetStatus[] = ['RUNNING', 'STANDBY', 'ALARM', 'OFFLINE', 'MAINTENANCE'];

/**
 * provinceFleetSummary aggregates devices + projects into a per-province fleet
 * status summary. Devices with no runtime binding (dataSource 'real', i.e.
 * planned/not-commissioned) are counted as PLANNED — never as RUNNING.
 */
export function provinceFleetSummary(
  devices: Device[],
  projects: Project[]
): Map<number, ProvinceFleet> {
  const map = new Map<number, ProvinceFleet>();
  const fleet = (code: number): ProvinceFleet => {
    let f = map.get(code);
    if (!f) {
      f = {
        adcode: code,
        total: 0,
        running: 0,
        standby: 0,
        alarm: 0,
        offline: 0,
        maintenance: 0,
        planned: 0,
        projects: 0,
        types: {},
      };
      map.set(code, f);
    }
    return f;
  };
  for (const d of devices) {
    const code = d.adminPath[0]!;
    const f = fleet(code);
    f.total++;
    const status: FleetStatus = d.dataSource === 'real' ? 'PLANNED' : (d.status as FleetStatus);
    if (status === 'PLANNED') f.planned++;
    else if (status === 'RUNNING') f.running++;
    else if (status === 'STANDBY') f.standby++;
    else if (status === 'ALARM') f.alarm++;
    else if (status === 'OFFLINE') f.offline++;
    else if (status === 'MAINTENANCE') f.maintenance++;
    f.types[d.craneModel] = (f.types[d.craneModel] ?? 0) + 1;
  }
  for (const p of projects) {
    const code = p.adminPath[0]!;
    fleet(code).projects++;
  }
  return map;
}

export const fleetStatusOrder: FleetStatus[] = [...RUNTIME_STATUSES, 'PLANNED'];
