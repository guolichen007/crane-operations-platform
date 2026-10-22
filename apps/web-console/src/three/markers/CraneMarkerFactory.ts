import {
  Group,
  BoxGeometry,
  CylinderGeometry,
  Mesh,
  MeshStandardMaterial,
  BufferGeometry,
  LineSegments,
  LineBasicMaterial,
  Vector3,
  MeshBasicMaterial,
} from 'three';
import { mergeGeometries } from 'three/addons/utils/BufferGeometryUtils.js';
export function createCrane(color: string, scale = 1) {
  const g = new Group(),
    metal = new MeshStandardMaterial({
      color,
      roughness: 0.4,
      metalness: 0.5,
      emissive: color,
      emissiveIntensity: 0.12,
    });
  const pieces: BufferGeometry[] = [];
  const box = (x: number, y: number, z: number, w: number, h: number, d: number) => {
    pieces.push(new BoxGeometry(w, h, d).translate(x, y, z));
  };
  box(-0.68, 0.5, 0, 0.13, 1, 0.55);
  box(0.68, 0.5, 0, 0.13, 1, 0.55);
  box(0, 1.04, -0.17, 1.7, 0.15, 0.13);
  box(0, 1.04, 0.17, 1.7, 0.15, 0.13);
  box(0.13, 0.92, 0, 0.35, 0.15, 0.45);
  const ropes = new BufferGeometry().setFromPoints([
    new Vector3(0.06, 0.9, 0),
    new Vector3(0.06, 0.35, 0),
    new Vector3(0.2, 0.9, 0),
    new Vector3(0.2, 0.35, 0),
  ]);
  g.add(new LineSegments(ropes, new LineBasicMaterial({ color })));
  pieces.push(new CylinderGeometry(0.11, 0.22, 0.24, 4, 1, true).translate(0.13, 0.25, 0));
  const body = new Mesh(mergeGeometries(pieces), metal);
  body.castShadow = true;
  g.add(body);
  for (const geometry of pieces) geometry.dispose();
  const beacon = new Mesh(
    new CylinderGeometry(0.055, 0.055, 0.15, 8),
    new MeshBasicMaterial({ color })
  );
  beacon.position.set(0.68, 1.18, 0);
  g.add(beacon);
  g.userData.beacon = beacon;
  beacon.material.transparent = true;
  beacon.material.toneMapped = false;
  g.scale.setScalar(scale);
  return g;
}
