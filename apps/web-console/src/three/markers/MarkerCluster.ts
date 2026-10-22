import type { Device } from '../../data/types';
import type { Project } from '../../data/types';
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
