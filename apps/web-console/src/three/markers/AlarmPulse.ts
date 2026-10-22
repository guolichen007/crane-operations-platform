import { Group, Mesh, RingGeometry, MeshBasicMaterial } from 'three';
export class AlarmPulse {
  group = new Group();
  constructor(scale = 1) {
    for (let i = 0; i < 3; i++) {
      const ring = new Mesh(
        new RingGeometry(0.5, 0.62, 48),
        new MeshBasicMaterial({
          color: 0xff665f,
          transparent: true,
          opacity: 0.4,
          depthWrite: false,
        })
      );
      ring.rotation.x = -Math.PI / 2;
      ring.position.y = 0.04;
      ring.userData.phase = i / 3;
      ring.scale.setScalar(scale);
      this.group.add(ring);
    }
    this.group.userData.scale = scale;
  }
  update(t: number) {
    for (const r of this.group.children as Mesh<RingGeometry, MeshBasicMaterial>[]) {
      const p = (t / 2.2 + r.userData.phase) % 1;
      r.scale.setScalar((0.3 + p * 2.5) * this.group.userData.scale);
      r.material.opacity = (1 - p) * 0.65;
    }
  }
}
