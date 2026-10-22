import { Group } from 'three';
import { createCrane } from './CraneMarkerFactory';
import { ProjectBeacon } from './ProjectBeacon';
import { project } from '../map/GeoProjection';
import type { Project, Device } from '../../data/types';
import { statusColor } from '../../data/types';
export function projectMarker(p: Project, device?: Device, index = 0, height = 1.5, scale = 1) {
  const group = new Group();
  const [x, z] = project(p.approximateLng, p.approximateLat);
  const angle = index * 2.1,
    offset = device && p.quantity > 1 ? scale * 2.1 : 0;
  group.position.set(x + Math.cos(angle) * offset, height, z + Math.sin(angle) * offset);
  const real = p.dataSource === 'real',
    color = real ? '#e8b75d' : statusColor[device?.status ?? 'RUNNING'];
  const beacon = new ProjectBeacon(real, p.quantity, scale, color);
  const crane = createCrane(color, scale * (real ? 1 : 0.7));
  crane.position.y = beacon.height;
  group.add(beacon.group, crane);
  Object.assign(group.userData, {
    crane,
    project: p,
    device,
    beacon,
    pickCenter: device ? beacon.height + scale * 0.65 : beacon.height * 0.65,
    labelHeight: beacon.height + scale * 1.7,
    hovered: false,
  });
  return group;
}
