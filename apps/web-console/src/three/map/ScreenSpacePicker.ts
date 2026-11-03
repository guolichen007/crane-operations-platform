import { Vector3, type Object3D, type Camera } from 'three';
export type AssetPickKind = 'device' | 'project' | 'cluster';
export type PickTarget = {
  kind: AssetPickKind;
  id: string;
  object: Object3D;
  radius: number;
  centerY: number;
  spanY?: [number, number];
};
export type AssetPick = { kind: AssetPickKind; id: string; distancePx: number };
type ProjectedTarget = {
  kind: AssetPickKind;
  id: string;
  x: number;
  y: number;
  radius: number;
  span?: [{ x: number; y: number }, { x: number; y: number }];
};
const rank: Record<AssetPickKind, number> = { device: 0, project: 1, cluster: 2 };

/**
 * ScreenSpacePicker projects pick targets to screen space. The projection is a
 * cached index: it is rebuilt only when the camera moves (fingerprint) or the
 * target set changes (invalidate), never on every pointermove.
 */
export class ScreenSpacePicker {
  private point = new Vector3();
  private cache: { fingerprint: number; revision: number; rect: DOMRect; targets: ProjectedTarget[] } | null = null;
  private revision = 0;
  constructor(
    private element: HTMLElement,
    private camera: Camera,
    private targets: () => PickTarget[]
  ) {}
  private visible(object: Object3D) {
    let node: Object3D | null = object;
    while (node) {
      if (!node.visible) return false;
      node = node.parent;
    }
    return true;
  }
  /** The target set changed (markers rebuilt / level changed / resize). */
  invalidate() {
    this.revision++;
    this.cache = null;
  }
  private fingerprint(): number {
    // Cheap numeric key over camera position/orientation + projection scale so a
    // static camera reuses the cached projection across many pointer moves.
    const m = this.camera.matrixWorld.elements;
    const p = this.camera.projectionMatrix.elements;
    const v = [m[0], m[4], m[8], m[12], m[13], m[14], p[0], p[5]];
    let h = 0;
    for (let i = 0; i < v.length; i++) h = (h * 31 + Math.round(v[i]! * 1e3)) | 0;
    return h;
  }
  snapshot(): ProjectedTarget[] {
    const rect = this.element.getBoundingClientRect();
    const fp = this.fingerprint();
    if (this.cache && this.cache.fingerprint === fp && this.cache.revision === this.revision) {
      // The rect may shift (scroll/layout) without the camera moving; re-anchor.
      if (
        this.cache.rect.left === rect.left &&
        this.cache.rect.top === rect.top &&
        this.cache.rect.width === rect.width &&
        this.cache.rect.height === rect.height
      )
        return this.cache.targets;
    }
    const projected = this.targets()
      .filter((t) => this.visible(t.object))
      .flatMap((t): ProjectedTarget[] => {
        t.object.localToWorld(this.point.set(0, t.centerY, 0));
        this.point.project(this.camera);
        if (
          this.point.z < -1 ||
          this.point.z > 1 ||
          Math.abs(this.point.x) > 1 ||
          Math.abs(this.point.y) > 1
        )
          return [];
        const span = t.spanY?.map(
          (y): { x: number; y: number } => {
            t.object.localToWorld(this.point.set(0, y, 0));
            this.point.project(this.camera);
            return {
              x: rect.left + ((this.point.x + 1) * rect.width) / 2,
              y: rect.top + ((1 - this.point.y) * rect.height) / 2,
            };
          }
        ) as [{ x: number; y: number }, { x: number; y: number }] | undefined;
        t.object.localToWorld(this.point.set(0, t.centerY, 0));
        this.point.project(this.camera);
        return [
          {
            kind: t.kind,
            id: t.id,
            x: rect.left + ((this.point.x + 1) * rect.width) / 2,
            y: rect.top + ((1 - this.point.y) * rect.height) / 2,
            radius: t.radius,
            span,
          },
        ];
      });
    this.cache = { fingerprint: fp, revision: this.revision, rect, targets: projected };
    return projected;
  }
  pick(x: number, y: number): AssetPick | null {
    const hits = this.snapshot()
      .map((t) => {
        let distancePx = Math.hypot(t.x - x, t.y - y);
        if (t.span) {
          const [a, b] = t.span;
          const dx = b.x - a.x,
            dy = b.y - a.y,
            length = dx * dx + dy * dy;
          const u = length
            ? Math.max(0, Math.min(1, ((x - a.x) * dx + (y - a.y) * dy) / length))
            : 0;
          distancePx = Math.min(distancePx, Math.hypot(x - a.x - u * dx, y - a.y - u * dy));
        }
        return { ...t, distancePx };
      })
      .filter((t) => t.distancePx <= t.radius);
    hits.sort((a, b) => rank[a.kind] - rank[b.kind] || a.distancePx - b.distancePx);
    const first = hits[0];
    return first ? { kind: first.kind, id: first.id, distancePx: first.distancePx } : null;
  }
}
