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
const rank: Record<AssetPickKind, number> = { device: 0, project: 1, cluster: 2 };
export class ScreenSpacePicker {
  private point = new Vector3();
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
  snapshot() {
    const rect = this.element.getBoundingClientRect();
    return this.targets()
      .filter((t) => this.visible(t.object))
      .flatMap((t) => {
        t.object.localToWorld(this.point.set(0, t.centerY, 0));
        this.point.project(this.camera);
        if (
          this.point.z < -1 ||
          this.point.z > 1 ||
          Math.abs(this.point.x) > 1 ||
          Math.abs(this.point.y) > 1
        )
          return [];
        const span = t.spanY?.map((y) => {
          t.object.localToWorld(this.point.set(0, y, 0));
          this.point.project(this.camera);
          return {
            x: rect.left + ((this.point.x + 1) * rect.width) / 2,
            y: rect.top + ((1 - this.point.y) * rect.height) / 2,
          };
        });
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
  }
  pick(x: number, y: number): AssetPick | null {
    const hits = this.snapshot()
      .map((t) => {
        let distancePx = Math.hypot(t.x - x, t.y - y);
        if (t.span) {
          const [a, b] = t.span as [{ x: number; y: number }, { x: number; y: number }];
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
