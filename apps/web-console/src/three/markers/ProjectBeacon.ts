import {
  Group,
  Mesh,
  CylinderGeometry,
  RingGeometry,
  MeshBasicMaterial,
  MeshStandardMaterial,
  AdditiveBlending,
} from 'three';
const stemGeometry = new CylinderGeometry(1, 1, 1, 8);
const ringA = new RingGeometry(0.78, 0.82, 48, 1, 0.12, Math.PI * 1.65);
const ringB = new RingGeometry(1.02, 1.045, 56, 1, 0.6, Math.PI * 1.78);
for (const g of [stemGeometry, ringA, ringB]) g.userData.shared = true;
export class ProjectBeacon {
  group = new Group();
  stem: Mesh;
  rings: Mesh<RingGeometry, MeshBasicMaterial>[] = [];
  private hover = 0;
  private restCraneScale?: number;
  private baseOpacity: number;
  readonly height: number;
  constructor(
    real: boolean,
    quantity: number,
    readonly scale: number,
    color: string
  ) {
    this.baseOpacity = real ? 0.45 : 0.3;
    this.height = ((real ? 0.65 + Math.min(2, quantity - 1) * 0.175 : 0.3) * scale) / 0.7;
    const stem = new Mesh(
      stemGeometry,
      new MeshBasicMaterial({
        color: real ? '#d3ae68' : color,
        transparent: true,
        opacity: this.baseOpacity,
        depthWrite: false,
        toneMapped: false,
      })
    );
    const radius = ((real ? 0.02 : 0.016) * scale) / 0.7;
    stem.scale.set(radius, this.height, radius);
    stem.position.y = this.height / 2;
    this.stem = stem;
    this.group.add(stem);
    for (const [i, geometry] of (real ? [ringA, ringB] : [ringA]).entries()) {
      const ring = new Mesh(
        geometry,
        new MeshBasicMaterial({
          color,
          transparent: true,
          opacity: 0.12,
          depthWrite: false,
          toneMapped: false,
          blending: AdditiveBlending,
        })
      );
      ring.rotation.x = -Math.PI / 2;
      ring.position.y = 0.025 + i * 0.018;
      ring.scale.setScalar(scale * (real ? 1 : 0.7));
      this.group.add(ring);
      this.rings.push(ring);
    }
  }
  update(t: number, dt: number, hovered: boolean, selected: boolean, crane: Group) {
    this.hover += (Number(hovered) - this.hover) * Math.min(1, dt * 14);
    const h = this.height;
    this.restCraneScale ??= crane.scale.x;
    crane.scale.setScalar(this.restCraneScale * (1 + this.hover * 0.04));
    (this.stem.material as MeshBasicMaterial).opacity =
      this.baseOpacity + (0.75 - this.baseOpacity) * this.hover;
    this.stem.scale.y = h;
    this.stem.position.y = h / 2;
    crane.position.y = h;
    for (const [i, ring] of this.rings.entries()) {
      ring.rotation.z = t * (i === 0 ? (Math.PI * 2) / 10 : (-Math.PI * 2) / 17);
      ring.material.opacity = (selected ? 0.45 : 0.12) + this.hover * (selected ? 0 : 0.18);
    }
    for (const child of crane.children)
      if (child instanceof Mesh && child.material instanceof MeshStandardMaterial)
        child.material.emissiveIntensity = 0.12 + this.hover * 0.06;
  }
  snapshot() {
    return {
      height: this.stem.scale.y,
      hover: this.hover,
      rings: this.rings.map((r) => ({ angle: r.rotation.z, opacity: r.material.opacity })),
    };
  }
}
