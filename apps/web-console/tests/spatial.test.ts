import { describe, it, expect } from 'vitest';
import { Mesh, OrthographicCamera, Group } from 'three';
import type { Feature } from 'geojson';
import { buildRegion } from '../src/three/map/RegionMeshBuilder';
import { ScreenSpacePicker } from '../src/three/map/ScreenSpacePicker';
import { ProjectBeacon } from '../src/three/markers/ProjectBeacon';
import { disposeObject } from '../src/three/utils/dispose';
describe('立体几何与全柱拾取', () => {
  it('主体有独立倒角且微小岛屿不变成同高针柱', () => {
    const f: Feature = {
      type: 'Feature',
      properties: { adcode: 1 },
      geometry: {
        type: 'MultiPolygon',
        coordinates: [
          [
            [
              [110, 25],
              [114, 25],
              [114, 29],
              [110, 29],
              [110, 25],
            ],
          ],
          [
            [
              [115, 25],
              [115.0001, 25],
              [115.0001, 25.0001],
              [115, 25.0001],
              [115, 25],
            ],
          ],
        ],
      },
    };
    const g = buildRegion(f);
    const bevel = g.getObjectByName('micro-bevel') as Mesh;
    expect(bevel.geometry.getAttribute('position').count).toBeGreaterThan(0);
    const side = g.children[0] as Mesh,
      pos = side.geometry.getAttribute('position');
    for (let i = 0; i < pos.count; i++)
      expect(Number.isFinite(pos.getX(i) + pos.getY(i) + pos.getZ(i))).toBe(true);
    // Island wall vertices stay within a tiny strip near the top, not down at the country base.
    const islandZ: number[] = [];
    for (let i = 0; i < pos.count; i++) if (pos.getX(i) > 15.74) islandZ.push(pos.getZ(i));
    expect(Math.max(...islandZ) - Math.min(...islandZ)).toBeLessThan(0.01);
    disposeObject(g);
  });
  it('Beacon 的柱端也能被拾取并保持设备优先级', () => {
    const camera = new OrthographicCamera(-10, 10, 10, -10, 0.1, 100);
    camera.position.z = 20;
    camera.lookAt(0, 0, 0);
    camera.updateMatrixWorld();
    const object = new Group();
    object.updateMatrixWorld();
    const el = {
      getBoundingClientRect: () => ({ left: 0, top: 0, width: 1000, height: 1000 }),
    } as HTMLElement;
    const picker = new ScreenSpacePicker(el, camera, () => [
      { kind: 'project', id: 'p', object, radius: 38, centerY: 4 },
      { kind: 'device', id: 'd', object, radius: 38, centerY: 2, spanY: [0, 4] },
    ]);
    expect(picker.pick(500, 305)?.id).toBe('d');
    expect(picker.pick(500, 495)?.id).toBe('d');
  });
  it('销毁一个项目不会销毁其余 Beacon 的共享几何', () => {
    const a = new ProjectBeacon(true, 3, 0.7, '#e8b75d'),
      b = new ProjectBeacon(true, 1, 0.7, '#e8b75d');
    let disposed = false;
    b.rings[0]!.geometry.addEventListener('dispose', () => {
      disposed = true;
    });
    disposeObject(a.group);
    expect(disposed).toBe(false);
    expect(b.rings[0]!.geometry.getAttribute('position').count).toBeGreaterThan(0);
    disposeObject(b.group);
  });
});
