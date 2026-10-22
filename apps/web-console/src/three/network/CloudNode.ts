import {
  Group,
  Mesh,
  IcosahedronGeometry,
  MeshBasicMaterial,
  RingGeometry,
  CylinderGeometry,
  AdditiveBlending,
} from 'three';
export class CloudNode {
  group = new Group();
  private core = new Mesh(
    new IcosahedronGeometry(0.35, 1),
    new MeshBasicMaterial({ color: 0x9deaf0, wireframe: true, toneMapped: false })
  );
  private rings: Mesh[] = [];
  angle = 0;
  constructor() {
    this.group.scale.setScalar(0.85);
    for (const [i, r] of [0.75, 1.08].entries()) {
      const ring = new Mesh(
        new RingGeometry(r, r + 0.018, 64, 1, 0.2, Math.PI * 1.8),
        new MeshBasicMaterial({
          color: 0x7fcdd5,
          transparent: true,
          opacity: i ? 0.12 : 0.24,
          depthWrite: false,
          blending: AdditiveBlending,
          toneMapped: false,
        })
      );
      ring.rotation.x = i ? -0.95 : -Math.PI / 2;
      this.rings.push(ring);
      this.group.add(ring);
    }
    const column = new Mesh(
      new CylinderGeometry(0.018, 0.018, 6.2, 6),
      new MeshBasicMaterial({
        color: 0x6bc4d0,
        transparent: true,
        opacity: 0.10,
        depthWrite: false,
      })
    );
    column.position.y = -3.1;
    this.group.add(this.core, column);
  }
  update(t: number) {
    this.angle = (t * Math.PI * 2) / 18;
    this.rings[0]!.rotation.z = this.angle;
    this.rings[1]!.rotation.z = (-t * Math.PI * 2) / 25;
    this.core.rotation.set(t * 0.06, t * 0.12, t * 0.03);
  }
}
