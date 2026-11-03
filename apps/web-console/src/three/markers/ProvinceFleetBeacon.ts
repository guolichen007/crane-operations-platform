import {
  Group,
  Mesh,
  BufferGeometry,
  Float32BufferAttribute,
  MeshBasicMaterial,
  Color,
} from 'three';
import { statusColor } from '../../data/types';
import type { FleetStatus, ProvinceFleet } from './MarkerCluster';
import { fleetStatusOrder } from './MarkerCluster';

const PLANNED_COLOR = '#e8b75d';

function fleetColor(status: FleetStatus): string {
  return status === 'PLANNED' ? PLANNED_COLOR : statusColor[status];
}

function fleetCount(fleet: ProvinceFleet, status: FleetStatus): number {
  switch (status) {
    case 'RUNNING':
      return fleet.running;
    case 'STANDBY':
      return fleet.standby;
    case 'ALARM':
      return fleet.alarm;
    case 'OFFLINE':
      return fleet.offline;
    case 'MAINTENANCE':
      return fleet.maintenance;
    case 'PLANNED':
      return fleet.planned;
  }
}

/**
 * Builds one flat ring mesh with per-segment vertex colors: a single draw call
 * encodes the whole status distribution, never one mesh per status.
 */
function fleetRingGeometry(fleet: ProvinceFleet, scale: number): BufferGeometry {
  const inner = 0.9 * scale;
  const outer = 1.12 * scale;
  const total = fleet.total;
  const statuses = fleetStatusOrder.filter((s) => fleetCount(fleet, s) > 0 && total > 0);
  const positions: number[] = [];
  const colors: number[] = [];
  const index: number[] = [];
  const color = new Color();
  let cursor = 0; // vertex cursor
  let start = 0;
  for (const status of statuses) {
    const length = (fleetCount(fleet, status) / total) * Math.PI * 2;
    const steps = Math.max(2, Math.ceil((length / (Math.PI * 2)) * 48));
    color.set(fleetColor(status));
    const base = cursor;
    for (let i = 0; i <= steps; i++) {
      const theta = start + (i / steps) * length;
      const c = Math.cos(theta);
      const s = Math.sin(theta);
      positions.push(c * inner, 0.06, s * inner, c * outer, 0.06, s * outer);
      colors.push(color.r, color.g, color.b, color.r, color.g, color.b);
      cursor += 2;
    }
    for (let i = 0; i < steps; i++) {
      const a = base + i * 2;
      const b = a + 1;
      const c = a + 2;
      const d = a + 3;
      index.push(a, c, b, b, c, d);
    }
    start += length;
  }
  const geometry = new BufferGeometry();
  geometry.setAttribute('position', new Float32BufferAttribute(positions, 3));
  geometry.setAttribute('color', new Float32BufferAttribute(colors, 3));
  geometry.setIndex(index);
  return geometry;
}

/**
 * ProvinceFleetBeacon is the country-level province status ring rendered as a
 * single vertex-colored mesh (one draw call) whose arc lengths encode the
 * province status distribution. The central total is a DOM label, not a glyph.
 */
export class ProvinceFleetBeacon {
  group = new Group();
  private mesh: Mesh<BufferGeometry, MeshBasicMaterial>;
  private baseOpacity = 0.85;

  constructor(fleet: ProvinceFleet, scale = 1) {
    const geometry = fleetRingGeometry(fleet, scale);
    const material = new MeshBasicMaterial({
      vertexColors: true,
      transparent: true,
      opacity: this.baseOpacity,
      depthWrite: false,
      toneMapped: false,
    });
    this.mesh = new Mesh(geometry, material);
    this.mesh.rotation.x = -Math.PI / 2;
    this.group.add(this.mesh);
  }

  update(selected: boolean, hovered: boolean) {
    this.mesh.material.opacity = this.baseOpacity + (hovered ? 0.15 : 0);
    this.group.position.y = selected ? 0.06 : 0;
  }

  dispose() {
    this.group.removeFromParent();
    this.mesh.geometry.dispose();
    this.mesh.material.dispose();
  }
}

/**
 * Builds every province fleet ring into ONE vertex-colored mesh: the country
 * fleet layer stays a single draw call regardless of the number of provinces.
 */
export function buildFleetBeaconLayer(
  items: { fleet: ProvinceFleet; position: [number, number, number] }[],
  scale = 0.16
): Mesh<BufferGeometry, MeshBasicMaterial> | null {
  const positions: number[] = [];
  const colors: number[] = [];
  const index: number[] = [];
  const color = new Color();
  let cursor = 0;
  const inner = 0.9 * scale;
  const outer = 1.12 * scale;
  for (const { fleet, position } of items) {
    const total = fleet.total;
    const statuses = fleetStatusOrder.filter((s) => fleetCount(fleet, s) > 0 && total > 0);
    if (!statuses.length) continue;
    let start = 0;
    for (const status of statuses) {
      const length = (fleetCount(fleet, status) / total) * Math.PI * 2;
      const steps = Math.max(2, Math.ceil((length / (Math.PI * 2)) * 48));
      color.set(fleetColor(status));
      const base = cursor;
      for (let i = 0; i <= steps; i++) {
        const theta = start + (i / steps) * length;
        const c = Math.cos(theta);
        const s = Math.sin(theta);
        positions.push(
          position[0] + c * inner,
          position[1],
          position[2] + s * inner,
          position[0] + c * outer,
          position[1],
          position[2] + s * outer
        );
        colors.push(color.r, color.g, color.b, color.r, color.g, color.b);
        cursor += 2;
      }
      for (let i = 0; i < steps; i++) {
        const a = base + i * 2;
        const b = a + 1;
        const c = a + 2;
        const d = a + 3;
        index.push(a, c, b, b, c, d);
      }
      start += length;
    }
  }
  if (!positions.length) return null;
  const geometry = new BufferGeometry();
  geometry.setAttribute('position', new Float32BufferAttribute(positions, 3));
  geometry.setAttribute('color', new Float32BufferAttribute(colors, 3));
  geometry.setIndex(index);
  const material = new MeshBasicMaterial({
    vertexColors: true,
    transparent: true,
    opacity: 0.85,
    depthWrite: false,
    toneMapped: false,
  });
  const mesh = new Mesh(geometry, material);
  mesh.name = 'province-fleet-beacons';
  mesh.rotation.x = -Math.PI / 2;
  return mesh;
}
