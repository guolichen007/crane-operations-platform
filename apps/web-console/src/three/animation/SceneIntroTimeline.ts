import gsap from 'gsap';
import { Group, type Object3D, type Material } from 'three';
function materials(root: Object3D) {
  const result = new Map<Material, number>();
  root.traverse((o) => {
    const m = (o as import('three').Mesh).material;
    for (const mat of m ? (Array.isArray(m) ? m : [m]) : []) result.set(mat, mat.opacity);
  });
  return result;
}
export class SceneIntroTimeline {
  private timeline = gsap.timeline({ paused: true });
  readonly duration = 1.9;
  active = true;
  constructor(
    private start: number,
    country: Group,
    underlay: Group,
    markers: Group,
    network: Group,
    labels: HTMLElement,
    reduced: boolean
  ) {
    const mapMaterials = materials(country),
      baseMaterials = materials(underlay),
      netMaterials = materials(network);
    const mapFade = { opacity: 0 },
      baseFade = { opacity: 0 },
      networkFade = { opacity: 0 };
    const apply = (list: Map<Material, number>, factor: number) => {
      for (const [m, base] of list) m.opacity = base * factor;
    };
    apply(mapMaterials, 0);
    apply(baseMaterials, 0);
    apply(netMaterials, 0);
    network.visible = false;
    labels.style.opacity = '0';
    this.timeline
      .fromTo(underlay.position, { y: -0.15 }, { y: 0, duration: 0.55, ease: 'power2.out' }, 0.1)
      .to(
        baseFade,
        { opacity: 1, duration: 0.55, onUpdate: () => apply(baseMaterials, baseFade.opacity) },
        0.1
      )
      .fromTo(country.position, { y: -0.12 }, { y: 0, duration: 0.6, ease: 'power2.out' }, 0.15)
      .to(
        mapFade,
        { opacity: 1, duration: 0.6, onUpdate: () => apply(mapMaterials, mapFade.opacity) },
        0.15
      )
      .to(labels, { opacity: 1, duration: 0.5 }, 0.55);
    const real = markers.children.filter((g) => g.userData.project?.dataSource === 'real');
    const demo = markers.children.filter((g) => g.userData.project?.dataSource === 'demo');
    real.forEach((g, i) =>
      this.timeline.fromTo(
        g.scale,
        { x: 0.75, y: 0, z: 0.75 },
        { x: 1, y: 1, z: 1, duration: 0.35, ease: 'power2.out' },
        0.85 + i * 0.04
      )
    );
    demo.forEach((g, i) =>
      this.timeline.fromTo(
        g.scale,
        { x: 0.5, y: 0, z: 0.5 },
        { x: 1, y: 1, z: 1, duration: 0.45, ease: 'power2.out' },
        1.1 + i * 0.02
      )
    );
    this.timeline.call(
      () => {
        network.visible = true;
      },
      [],
      1.3
    );
    this.timeline.to(
      networkFade,
      { opacity: 1, duration: 0.6, onUpdate: () => apply(netMaterials, networkFade.opacity) },
      1.3
    );
    if (reduced) this.finish();
  }
  update(t: number) {
    if (!this.active) return;
    this.timeline.time(Math.min(this.duration, Math.max(0, t - this.start)), false);
    if (t - this.start >= this.duration) this.finish();
  }
  finish() {
    if (!this.active) return;
    this.timeline.progress(1, false);
    this.active = false;
    this.timeline.kill();
  }
  dispose() {
    this.timeline.kill();
  }
}
