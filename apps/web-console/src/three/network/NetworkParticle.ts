import {
  Group,
  Mesh,
  SphereGeometry,
  MeshBasicMaterial,
  BufferGeometry,
  Float32BufferAttribute,
  Line,
  LineBasicMaterial,
  Vector3,
} from 'three';
import type { NetworkArc } from './NetworkArc';
export class NetworkParticle {
  group = new Group();
  mesh = new Mesh(
    new SphereGeometry(0.12, 8, 6),
    new MeshBasicMaterial({
      color: 0xc1faff,
      toneMapped: false,
      transparent: true,
      opacity: 0.65,
      depthWrite: false,
    })
  );
  private positions = new Float32Array(14 * 3);
  private geometry = new BufferGeometry();
  private point = new Vector3();
  constructor(
    private arc: NetworkArc,
    private phase: number
  ) {
    this.geometry.setAttribute('position', new Float32BufferAttribute(this.positions, 3));
    const colors: number[] = [];
    for (let i = 0; i < 14; i++) {
      const strength = Math.pow(1 - i / 14, 1.6);
      colors.push(0.55 * strength, 0.92 * strength, strength);
    }
    this.geometry.setAttribute('color', new Float32BufferAttribute(colors, 3));
    const tail = new Line(
      this.geometry,
      new LineBasicMaterial({
        vertexColors: true,
        transparent: true,
        opacity: 0.45,
        toneMapped: false,
        depthWrite: false,
      })
    );
    this.group.add(this.mesh, tail);
  }
  update(t: number) {
    const progress = (t * 0.14 + this.phase) % 1;
    this.arc.curve.getPoint(progress, this.mesh.position);
    const attribute = this.geometry.getAttribute('position');
    for (let i = 0; i < 14; i++) {
      this.arc.curve.getPoint(Math.max(0, progress - i * 0.0024), this.point);
      attribute.setXYZ(i, this.point.x, this.point.y, this.point.z);
    }
    attribute.needsUpdate = true;
  }
}
