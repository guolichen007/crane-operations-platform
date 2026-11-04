import { describe, it, expect } from 'vitest';
import { CraneTwinFactory } from '../src/three/twin/CraneTwinFactory';

// The twin must never clamp an unknown axis to the travel minimum (which would
// fake a field position at the limit) nor read an unknown grab as "closed".
describe('CraneTwinFactory 未知轴与抓斗的结构示意姿态', () => {
  it('有效轴值被夹取到有效行程内', () => {
    const twin = new CraneTwinFactory();
    twin.setAxes({ x: 5, y: 2, z: 3, grab: 0.5 });
    expect(twin.gantryXGroup.position.x).toBeCloseTo(5);
    expect(twin.trolleyYGroup.position.z).toBeCloseTo(2);
    expect(twin.hoistZGroup.position.y).toBeCloseTo(3);
    expect(twin.leftJaw.rotation.z).toBeCloseTo(-0.35); // open 0.5 → ±0.35
    expect(twin.rightJaw.rotation.z).toBeCloseTo(0.35);
  });
  it('NaN 轴使用中性结构示意姿态，不夹到行程下限', () => {
    const twin = new CraneTwinFactory();
    twin.setAxes({ x: NaN, y: NaN, z: NaN, grab: NaN });
    expect(twin.gantryXGroup.position.x).toBeCloseTo(0); // 不是 -12
    expect(twin.trolleyYGroup.position.z).toBeCloseTo(0); // 不是 -5
    expect(twin.hoistZGroup.position.y).toBeCloseTo(4); // 不是 1.5
    expect(twin.leftJaw.rotation.z).toBeCloseTo(-0.14); // 0.2 中性，不是闭合 0
  });
  it('undefined 轴保持中性结构示意姿态', () => {
    const twin = new CraneTwinFactory();
    twin.setAxes({ x: undefined, y: undefined, z: undefined, grab: undefined });
    expect(twin.gantryXGroup.position.x).toBeCloseTo(0);
    expect(twin.trolleyYGroup.position.z).toBeCloseTo(0);
    expect(twin.hoistZGroup.position.y).toBeCloseTo(4);
    expect(twin.leftJaw.rotation.z).toBeCloseTo(-0.14);
  });
  it('部分轴有效、部分未知时区分：有效轴用真实值，未知轴中性，未知抓斗不闭合', () => {
    const twin = new CraneTwinFactory();
    twin.setAxes({ x: 8, y: NaN, z: NaN, grab: 1 });
    expect(twin.gantryXGroup.position.x).toBeCloseTo(8); // 有效
    expect(twin.trolleyYGroup.position.z).toBeCloseTo(0); // 未知 → 中性
    expect(twin.hoistZGroup.position.y).toBeCloseTo(4); // 未知 → 中性
    expect(twin.leftJaw.rotation.z).toBeCloseTo(-0.7); // 明确打开
  });
});
