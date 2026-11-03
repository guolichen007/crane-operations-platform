import {
  Group,
  BoxGeometry,
  CylinderGeometry,
  InstancedMesh,
  Matrix4,
  MeshBasicMaterial,
  MeshStandardMaterial,
  Color,
  Quaternion,
  Vector3,
  type BufferGeometry,
} from 'three';
import { mergeGeometries } from 'three/addons/utils/BufferGeometryUtils.js';
import type { DeviceStatus } from '../../data/types';
import { statusColor } from '../../data/types';

export type GlyphItem = {
  assetId: string;
  projectId: string;
  position: [number, number, number];
  status: DeviceStatus;
  color: string;
};

const matrix = new Matrix4();
const quaternion = new Quaternion();
const one = new Vector3(1, 1, 1);
const colorTmp = new Color();

function craneGlyphGeometry(): BufferGeometry {
  const pieces: BufferGeometry[] = [];
  const box = (x: number, y: number, z: number, w: number, h: number, d: number) =>
    pieces.push(new BoxGeometry(w, h, d).translate(x, y, z));
  box(-0.68, 0.5, 0, 0.13, 1, 0.55); // left leg
  box(0.68, 0.5, 0, 0.13, 1, 0.55); // right leg
  box(0, 1.04, -0.17, 1.7, 0.15, 0.13); // bridge beam A
  box(0, 1.04, 0.17, 1.7, 0.15, 0.13); // bridge beam B
  box(0.13, 0.92, 0, 0.35, 0.15, 0.45); // trolley
  const merged = mergeGeometries(pieces)!;
  for (const g of pieces) g.dispose();
  return merged;
}

// Shared across every CraneGlyphLayer instance; never disposed per layer.
const glyphBodyGeometry = craneGlyphGeometry();
const glyphBeaconGeometry = new CylinderGeometry(0.055, 0.055, 0.15, 8);
glyphBodyGeometry.userData.shared = true;
glyphBeaconGeometry.userData.shared = true;

/**
 * CraneGlyphLayer renders the fleet as lightweight crane glyphs via two
 * InstancedMesh objects (body + status beacon), never one scene graph per crane.
 * Per-instance status is carried in instanceColor, not per-mesh materials.
 */
export class CraneGlyphLayer {
  readonly group = new Group();
  private body: InstancedMesh;
  private beacon: InstancedMesh;
  private bodyMaterial: MeshStandardMaterial;
  private beaconMaterial: MeshBasicMaterial;
  private items: GlyphItem[] = [];
  private assetIndex = new Map<string, number>();
  private phases: number[] = [];

  constructor(scale = 1) {
    this.bodyMaterial = new MeshStandardMaterial({
      color: '#8fa6bd',
      roughness: 0.45,
      metalness: 0.55,
    });
    this.beaconMaterial = new MeshBasicMaterial({ toneMapped: false, transparent: true });
    this.body = new InstancedMesh(glyphBodyGeometry, this.bodyMaterial, 0);
    this.body.castShadow = true;
    this.body.scale.setScalar(scale);
    this.beacon = new InstancedMesh(glyphBeaconGeometry, this.beaconMaterial, 0);
    this.beacon.scale.setScalar(scale);
    this.group.add(this.body, this.beacon);
  }

  setItems(items: GlyphItem[]) {
    this.items = items;
    this.assetIndex.clear();
    this.phases = items.map((_, i) => (i * 0.73) % (Math.PI * 2));
    this.body.count = items.length;
    this.beacon.count = items.length;
    const origin = new Vector3();
    items.forEach((item, i) => {
      this.assetIndex.set(item.assetId, i);
      matrix.compose(origin.set(item.position[0], item.position[1], item.position[2]), quaternion, one);
      this.body.setMatrixAt(i, matrix);
      // Beacon sits above the glyph body.
      matrix.compose(
        origin.set(item.position[0] + 0.68, item.position[1] + 1.18, item.position[2]),
        quaternion,
        one
      );
      this.beacon.setMatrixAt(i, matrix);
      colorTmp.set(item.color);
      this.beacon.setColorAt(i, colorTmp);
    });
    this.body.instanceMatrix.needsUpdate = true;
    this.beacon.instanceMatrix.needsUpdate = true;
    if (this.beacon.instanceColor) this.beacon.instanceColor.needsUpdate = true;
    this.body.visible = items.length > 0;
    this.beacon.visible = items.length > 0;
  }

  setStatus(assetId: string, status: DeviceStatus) {
    const index = this.assetIndex.get(assetId);
    if (index === undefined) return;
    const item = this.items[index];
    if (!item) return;
    item.status = status;
    item.color = statusColor[status];
    colorTmp.set(item.color);
    this.beacon.setColorAt(index, colorTmp);
    if (this.beacon.instanceColor) this.beacon.instanceColor.needsUpdate = true;
  }

  /** Low-frequency alarm pulse only; running beacons stay steady (no fast flicker). */
  update(t: number, reduced: boolean) {
    if (reduced) return;
    const color = this.beacon.instanceColor;
    if (!color) return;
    for (let i = 0; i < this.items.length; i++) {
      if (this.items[i]?.status !== 'ALARM') continue;
      const pulse = 0.55 + 0.45 * Math.sin(t * Math.PI * 1.2 + this.phases[i]!);
      colorTmp.set('#ff665f');
      color.setXYZ(i, colorTmp.r * pulse, colorTmp.g * pulse, colorTmp.b * pulse);
    }
    color.needsUpdate = true;
  }

  dispose() {
    this.group.removeFromParent();
    this.body.dispose();
    this.beacon.dispose();
    this.bodyMaterial.dispose();
    this.beaconMaterial.dispose();
  }
}
