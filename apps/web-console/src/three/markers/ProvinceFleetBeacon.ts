import { Group, Mesh, RingGeometry, MeshBasicMaterial } from 'three';
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
 * ProvinceFleetBeacon is the country-level province status ring: a segmented
 * ring whose arc lengths encode the province's status distribution. The central
 * total is rendered as a DOM label by the label layer, not as a 3D glyph.
 */
export class ProvinceFleetBeacon {
  group = new Group();
  private rings: Mesh<RingGeometry, MeshBasicMaterial>[] = [];
  private baseOpacity = 0.85;

  constructor(fleet: ProvinceFleet, scale = 1) {
    const inner = 0.9 * scale;
    const outer = 1.12 * scale;
    let start = 0;
    for (const status of fleetStatusOrder) {
      const count = fleetCount(fleet, status);
      if (count <= 0 || fleet.total <= 0) continue;
      const thetaLength = (count / fleet.total) * Math.PI * 2;
      const ring = new Mesh(
        new RingGeometry(inner, outer, 48, 1, start, thetaLength),
        new MeshBasicMaterial({
          color: fleetColor(status),
          transparent: true,
          opacity: this.baseOpacity,
          depthWrite: false,
          toneMapped: false,
        })
      );
      ring.rotation.x = -Math.PI / 2;
      ring.position.y = 0.06;
      ring.userData.status = status;
      this.group.add(ring);
      this.rings.push(ring);
      start += thetaLength;
    }
  }

  update(selected: boolean, hovered: boolean) {
    const opacity = this.baseOpacity + (hovered ? 0.15 : 0);
    for (const ring of this.rings) ring.material.opacity = opacity;
    // Selected provinces lift their ring slightly; color stays the real status.
    this.group.position.y = selected ? 0.06 : 0;
  }

  dispose() {
    this.group.removeFromParent();
    for (const ring of this.rings) {
      ring.geometry.dispose();
      ring.material.dispose();
    }
  }
}
