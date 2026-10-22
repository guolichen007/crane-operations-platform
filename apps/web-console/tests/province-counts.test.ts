import { describe, it, expect } from 'vitest';
import { devices } from '../src/data';
import { provinceDeviceCounts } from '../src/three/markers/MarkerCluster';
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
