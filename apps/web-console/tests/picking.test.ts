import { describe, it, expect } from 'vitest';
import { OrthographicCamera, Group } from 'three';
import { ScreenSpacePicker, type PickTarget } from '../src/three/map/ScreenSpacePicker';
import { NetworkLayer } from '../src/three/network/NetworkLayer';
import { projects, realProjects } from '../src/data';
const element = {
  getBoundingClientRect: () => ({ left: 0, top: 0, width: 1000, height: 1000 }),
} as HTMLElement;
function setup() {
  const camera = new OrthographicCamera(-10, 10, 10, -10, 0.1, 100);
  camera.position.set(0, 0, 20);
  camera.lookAt(0, 0, 0);
  camera.updateMatrixWorld();
  return camera;
}
function target(kind: PickTarget['kind'], x = 0): PickTarget {
  const object = new Group();
  object.position.x = x;
  object.updateMatrixWorld();
  return { kind, id: kind, object, radius: 38, centerY: 0 };
}
describe('资产优先拾取契约', () => {
  it('具体行车优先于更近的项目和聚合', () => {
    const picker = new ScreenSpacePicker(element, setup(), () => [
      target('cluster'),
      target('project'),
      target('device', 0.5),
    ]);
    expect(picker.pick(500, 500)?.kind).toBe('device');
  });
  it('项目优先于更近的聚合', () => {
    const picker = new ScreenSpacePicker(element, setup(), () => [
      target('cluster'),
      target('project', 0.4),
    ]);
    expect(picker.pick(500, 500)?.kind).toBe('project');
  });
  it('38px 内命中，界外留给后续行政区拾取', () => {
    const picker = new ScreenSpacePicker(element, setup(), () => [target('project')]);
    expect(picker.pick(537.9, 500)?.id).toBe('project');
    expect(picker.pick(539, 500)).toBeNull();
  });
  it('不可见资产不抢夺点击', () => {
    const device = target('device');
    device.object.visible = false;
    const picker = new ScreenSpacePicker(element, setup(), () => [device, target('project')]);
    expect(picker.pick(500, 500)?.kind).toBe('project');
  });
});
describe('通信数据语义', () => {
  it('真实未投运项目全部为计划虚线，不产生通信粒子', () => {
    const layer = new NetworkLayer(realProjects);
    layer.update(1);
    expect(layer.snapshot().plannedCount).toBe(6);
    expect(layer.snapshot().dynamicCount).toBe(0);
  });
  it('全国线路有限且 5.5 秒轮换', () => {
    const layer = new NetworkLayer(projects);
    layer.update(1);
    expect(layer.arcCount).toBe(10);
    expect(layer.particles.length).toBe(6);
    const first = [...layer.routeIds];
    layer.update(6);
    expect(layer.routeIds).not.toEqual(first);
    expect(layer.arcCount).toBeLessThanOrEqual(10);
  });
});
