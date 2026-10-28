import { Group, BoxGeometry, CylinderGeometry, Mesh, MeshStandardMaterial, MeshBasicMaterial, InstancedMesh, Matrix4, Vector3, Quaternion } from 'three';
import { disposeObject } from '../utils/dispose';
import type { QualityTier } from '../core/QualityManager';
export interface TwinAxes { x: number; y: number; z: number; grab: number }
export class CraneTwinFactory {
  root = new Group();
  runwayGroup = new Group(); gantryXGroup = new Group(); trolleyYGroup = new Group(); hoistZGroup = new Group(); grabGGroup = new Group();
  private ropes = new Group();
  private leftJaw = new Group(); private rightJaw = new Group();
  private structural = new MeshStandardMaterial({ color: '#d8a14c', roughness: .48, metalness: .65 });
  private steel = new MeshStandardMaterial({ color: '#526981', roughness: .5, metalness: .75 });
  private dark = new MeshStandardMaterial({ color: '#14263b', roughness: .6, metalness: .55 });
  constructor() {
    this.root.name = 'CraneTwinRoot';
    for (const [group, name] of [[this.runwayGroup, 'runwayGroup'], [this.gantryXGroup, 'gantryXGroup'], [this.trolleyYGroup, 'trolleyYGroup'], [this.hoistZGroup, 'hoistZGroup'], [this.grabGGroup, 'grabGGroup'], [this.ropes, 'ropes']] as const) group.name = name;
    this.root.add(this.runwayGroup, this.gantryXGroup); this.gantryXGroup.add(this.trolleyYGroup); this.trolleyYGroup.add(this.hoistZGroup, this.ropes); this.hoistZGroup.add(this.grabGGroup);
    for (const z of [-6, 6]) {
      this.box(this.runwayGroup, [0, .12, z], [30, .24, .24], this.steel);
      this.box(this.runwayGroup, [0, -.1, z], [31, .18, 1.1], this.dark);
      for (const x of [-1.9, 1.9]) {
        this.box(this.gantryXGroup, [x, 4.1, z], [.35, 7.9, .42], this.structural);
        this.box(this.gantryXGroup, [x, .65, z], [.85, .6, 1.05], this.dark);
      }
      this.box(this.gantryXGroup, [0, 1.05, z], [5.2, .45, .65], this.structural);
      for (const x of [-2.1, 2.1]) this.box(this.gantryXGroup, [x, 8.5, 0], [.6, .95, 14], this.structural);
      this.box(this.gantryXGroup, [0, 8.5, z], [4.5, .45, .8], this.structural);
    }
    // Repeated runway sleepers and handrail uprights use two draw calls.
    this.instances(this.runwayGroup, new BoxGeometry(.24, .12, 1.8), this.steel, Array.from({ length: 62 }, (_, i) => new Vector3(-15 + i % 31, -.02, i < 31 ? -6 : 6)));
    this.instances(this.gantryXGroup, new BoxGeometry(.055, .85, .055), this.structural, Array.from({ length: 52 }, (_, i) => new Vector3(i < 26 ? -2.55 : 2.55, 9.4, -6.5 + i % 26 * .5)));
    for (const x of [-2.55, 2.55]) { this.box(this.gantryXGroup, [x, 9.8, 0], [.06, .06, 13.5], this.structural); this.box(this.gantryXGroup, [x, 9.4, 0], [.06, .05, 13.5], this.structural); }
    this.box(this.trolleyYGroup, [0, 9.15, 0], [3.7, .5, 1.9], this.dark);
    this.box(this.trolleyYGroup, [0, 9.6, 0], [1.8, .65, 1.3], this.structural);
    this.box(this.gantryXGroup, [2.7, 7.2, -4.7], [1.4, 1.6, 1.7], this.dark);
    this.box(this.gantryXGroup, [3.42, 7.4, -4.7], [.02, .8, 1.2], new MeshStandardMaterial({ color: '#4599b9', metalness: .6, roughness: .2 }));
    for (const x of [-.65, .65]) for (const z of [-.45, .45]) {
      const rope = new Mesh(new CylinderGeometry(.025, .025, 1, 6), this.steel); rope.position.set(x, 0, z); this.ropes.add(rope);
    }
    this.box(this.hoistZGroup, [0, 0, 0], [1.55, .35, 1.2], this.structural);
    this.grabGGroup.position.y = -.45;
    this.grabGGroup.add(this.leftJaw, this.rightJaw);
    this.box(this.leftJaw, [-.45, -.4, 0], [.85, .65, 1.35], this.structural);
    this.box(this.rightJaw, [.45, -.4, 0], [.85, .65, 1.35], this.structural);
    const sensors = new Group(); sensors.name = 'sensorAnchors'; sensors.position.set(0, .3, 0); this.hoistZGroup.add(sensors);
    const lamp = new Mesh(new BoxGeometry(.16, .2, .16), new MeshBasicMaterial({ color: '#31d6e8' })); lamp.position.set(2.1, 10, -5.7); lamp.userData.bloom = true; lamp.name = 'beacon'; this.gantryXGroup.add(lamp);
    this.setAxes({ x: 0, y: 0, z: 4, grab: .2 });
  }
  private box(parent: Group, pos: number[], size: number[], material: MeshStandardMaterial) {
    const mesh = new Mesh(new BoxGeometry(size[0], size[1], size[2]), material); mesh.position.set(pos[0]!, pos[1]!, pos[2]!); mesh.castShadow = true; mesh.receiveShadow = true; parent.add(mesh); return mesh;
  }
  private instances(parent: Group, geometry: BoxGeometry, material: MeshStandardMaterial, points: Vector3[]) {
    const batch = new InstancedMesh(geometry, material, points.length), matrix = new Matrix4();
    points.forEach((p, i) => batch.setMatrixAt(i, matrix.compose(p, new Quaternion(), new Vector3(1, 1, 1)))); parent.add(batch);
  }
  setAxes(axes: Partial<TwinAxes>) {
    const clamp = (n: number, min: number, max: number) => Number.isFinite(n) ? Math.max(min, Math.min(max, n)) : min;
    if (axes.x !== undefined) this.gantryXGroup.position.x = clamp(axes.x, -12, 12);
    if (axes.y !== undefined) this.trolleyYGroup.position.z = clamp(axes.y, -5, 5);
    if (axes.z !== undefined) { const height = clamp(axes.z, 1.5, 8); this.hoistZGroup.position.y = height; this.ropes.children.forEach(r => { r.scale.y = 9 - height; r.position.y = (9 + height) / 2; }); }
    if (axes.grab !== undefined) { const open = clamp(axes.grab, 0, 1); this.leftJaw.rotation.z = -open * .7; this.rightJaw.rotation.z = open * .7; }
  }
  setHealth(state: 'normal' | 'alarm' | 'unknown') { this.structural.emissive.set(state === 'alarm' ? '#ff5d6c' : '#000000'); this.structural.emissiveIntensity = state === 'alarm' ? .15 : 0; }
  setSelection(selected: boolean) { this.root.userData.selected = selected; }
  setQuality(quality: QualityTier) { this.root.traverse(o => { if (o instanceof Mesh) o.castShadow = quality === 'premium'; }); }
  dispose() { disposeObject(this.root); }
}
