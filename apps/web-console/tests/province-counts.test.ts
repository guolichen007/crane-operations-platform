import { describe, it, expect } from 'vitest';
import { devices, projects } from '../src/data';
import { provinceDeviceCounts, provinceFleetSummary } from '../src/three/markers/MarkerCluster';
describe('省级设备汇总', () => {
  it('每台设备只计入所属省，真实设备与演示设备均不丢失', () => {
    const all = provinceDeviceCounts(devices);
    expect([...all.values()].reduce((s, n) => s + n, 0)).toBe(54);
    const real = provinceDeviceCounts(devices.filter((d) => d.dataSource === 'real'));
    expect([...real.values()].reduce((s, n) => s + n, 0)).toBe(10);
    expect(real.get(330000)).toBe(2);
    expect(real.get(530000)).toBe(3);
  });
  it('空筛选没有残留计数', () => expect(provinceDeviceCounts([]).size).toBe(0));
});
describe('省级机队态势', () => {
  it('真实未投运设备计入 planned，绝不当作 running', () => {
    const real = provinceFleetSummary(
      devices.filter((d) => d.dataSource === 'real'),
      []
    );
    expect(real.get(330000)).toMatchObject({ total: 2, planned: 2, running: 0 });
    expect(real.get(530000)).toMatchObject({ total: 3, planned: 3 });
  });
  it('total 是各状态之和，projects/types 有值', () => {
    const all = provinceFleetSummary(devices, projects);
    let total = 0;
    for (const f of all.values()) {
      total += f.total;
      expect(f.running + f.standby + f.alarm + f.offline + f.maintenance + f.planned).toBe(
        f.total
      );
    }
    expect(total).toBe(54);
    const zj = all.get(330000)!;
    expect(zj.projects).toBeGreaterThan(0);
    expect(Object.keys(zj.types).length).toBeGreaterThan(0);
  });
});
