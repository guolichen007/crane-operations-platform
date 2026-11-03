import { CSS2DObject, CSS2DRenderer } from 'three/addons/renderers/CSS2DRenderer.js';
import { Vector3, type Camera, type Object3D, type Scene } from 'three';
export type LabelKind = 'region' | 'real-project' | 'demo-cluster' | 'hover-card' | 'cloud';
type Label = {
  object: CSS2DObject;
  kind: LabelKind;
  priority: number;
  width?: number;
  height?: number;
  offset?: [number, number];
  sizeObserver?: ResizeObserver;
};
type Rect = { x: number; y: number; w: number; h: number };
const priorities: Record<LabelKind, number> = {
  'hover-card': 100,
  'real-project': 50,
  region: 30,
  'demo-cluster': 15,
  cloud: 10,
};
const HUD_SELECTOR =
  '.left-context,.right-context,.map-topline > *,.map-view-switch,.map-caption,.map-legend,.observation-switch';
export class MapLabelLayer {
  renderer = new CSS2DRenderer();
  labels: Label[] = [];
  /** Last label layout duration in ms, for the perf diagnostics hook. */
  lastLayoutMs = 0;
  private temp = new Vector3();
  private dragging = false;
  // HUD bounds are cached and only re-read on resize/layout change, never per frame.
  private hudBounds: Rect[] = [];
  private boundsDirty = true;
  private resizeObserver?: ResizeObserver;
  constructor(
    private host: HTMLElement,
    private pointerRoute?: (event: MouseEvent) => boolean
  ) {
    Object.assign(this.renderer.domElement.style, {
      position: 'absolute',
      inset: '0',
      pointerEvents: 'none',
    });
    host.append(this.renderer.domElement);
    if (typeof ResizeObserver !== 'undefined') {
      this.resizeObserver = new ResizeObserver(() => {
        this.boundsDirty = true;
      });
      this.resizeObserver.observe(host);
      if (host.parentElement) this.resizeObserver.observe(host.parentElement);
    }
  }
  private refreshBounds() {
    const host = this.host.getBoundingClientRect();
    const occupied: Rect[] = [];
    this.host.parentElement?.querySelectorAll(HUD_SELECTOR).forEach((panel) => {
      const r = panel.getBoundingClientRect();
      occupied.push({
        x: r.left - host.left + r.width / 2,
        y: r.top - host.top + r.height / 2,
        w: r.width + 6,
        h: r.height + 6,
      });
    });
    this.hudBounds = occupied;
    this.boundsDirty = false;
  }
  add(
    parent: Object3D,
    text: string,
    className: string,
    y: number,
    priority = 0,
    onClick?: () => void,
    kind?: LabelKind
  ) {
    kind ??= className.includes('hover-card')
      ? 'hover-card'
      : className.includes('map-region')
        ? 'region'
        : className.includes('real')
          ? 'real-project'
          : className.includes('cloud')
            ? 'cloud'
            : 'demo-cluster';
    const element = document.createElement('div');
    element.className = className;
    element.textContent = text;
    element.dataset.labelKind = kind;
    if (onClick) {
      element.style.pointerEvents = 'auto';
      element.tabIndex = 0;
      element.setAttribute('role', 'button');
      element.onclick = (e) => {
        e.stopPropagation();
        if (!this.pointerRoute?.(e)) onClick();
      };
      element.onkeydown = (e) => {
        if (e.key === 'Enter') {
          e.preventDefault();
          onClick();
        }
      };
    }
    const object = new CSS2DObject(element);
    object.position.y = y;
    parent.add(object);
    const label: Label = { object, kind, priority: priority > 10 ? priority : priorities[kind] };
    this.labels.push(label);
    // Invalidate the cached size when the element's content/layout changes; the
    // next render re-measures once, not every frame.
    if (typeof ResizeObserver !== 'undefined') {
      label.sizeObserver = new ResizeObserver(() => {
        label.width = undefined;
        label.height = undefined;
      });
      label.sizeObserver.observe(element);
    }
    return object;
  }
  clear() {
    for (const l of this.labels) {
      l.sizeObserver?.disconnect();
      l.object.element.remove();
      l.object.removeFromParent();
    }
    this.labels = [];
    this.boundsDirty = true;
  }
  /** During drag the collision layout is paused and labels keep their last offset. */
  setDragging(dragging: boolean) {
    this.dragging = dragging;
  }
  render(scene: Scene, camera: Camera, width: number, height: number) {
    const layoutStart = performance.now();
    if (this.boundsDirty) this.refreshBounds();
    const occupied: Rect[] = [...this.hudBounds];
    const priority = (l: Label) =>
      l.object.userData.forceVisible ? 90 : l.object.userData.selected ? 80 : l.priority;
    const ordered = [...this.labels].sort((a, b) => priority(b) - priority(a));
    for (const label of ordered) {
      const element = label.object.element;
      if (!label.object.visible) {
        element.style.visibility = 'hidden';
        continue;
      }
      // Measure a newly shown CSS2D card before placing it; CSS2D hides it with display:none.
      element.style.display = '';
      label.object.getWorldPosition(this.temp);
      this.temp.project(camera);
      const x = ((this.temp.x + 1) * width) / 2,
        y = ((1 - this.temp.y) * height) / 2;
      // Lazy size cache: measure only when not yet known (ResizeObserver resets it).
      if (label.width === undefined && element.offsetWidth) label.width = element.offsetWidth;
      if (label.height === undefined && element.offsetHeight) label.height = element.offsetHeight;
      const w = label.width ?? 40,
        h = label.height ?? 16;
      const offsets: [number, number][] = [[0, 0]];
      if (label.kind === 'region') {
        for (const d of [16, 28, 42, 58])
          offsets.push([0, -d], [0, d], [-d, 0], [d, 0], [-d, -d], [d, d], [d, -d], [-d, d]);
      } else if (label.kind === 'hover-card') {
        offsets.splice(0, 1, [0, -38], [70, -32], [-70, -32], [0, 32], [110, 0], [-110, 0]);
      } else {
        for (const d of [22, 40, 62, 86])
          offsets.push([0, -d], [0, d], [-d, 0], [d, 0], [-d, -22], [d, 22], [d, -22], [-d, 22]);
      }
      // Keep the previous valid placement instead of oscillating between candidates.
      if (label.offset && label.kind !== 'hover-card') offsets.unshift(label.offset);
      let chosen: [number, number] | undefined;
      if (this.dragging) {
        // Keep the previous placement while dragging; rebuild on settle.
        chosen = label.offset;
      } else if (this.temp.z >= -1 && this.temp.z <= 1)
        for (const [dx, dy] of offsets) {
          const cx = x + dx,
            cy = y + dy;
          if (cx - w / 2 < 4 || cx + w / 2 > width - 4 || cy - h / 2 < 4 || cy + h / 2 > height - 4)
            continue;
          if (
            !occupied.some(
              (b) =>
                Math.abs(cx - b.x) < (w + b.w) / 2 + 2 && Math.abs(cy - b.y) < (h + b.h) / 2 + 2
            )
          ) {
            chosen = [dx, dy];
            break;
          }
        }
      // Hovered names and details remain available even in dense areas.
      if (!chosen && (label.kind === 'hover-card' || label.object.userData.forceVisible))
        chosen = [
          Math.max(w / 2 + 4, Math.min(width - w / 2 - 4, x)) - x,
          Math.max(h / 2 + 4, Math.min(height - h / 2 - 4, y - 32)) - y,
        ];
      if (chosen) label.offset = chosen;
      element.style.visibility = chosen ? 'visible' : 'hidden';
      element.style.translate = `${chosen?.[0] ?? 0}px ${chosen?.[1] ?? 0}px`;
      element.dataset.displaced = String(!!chosen && (chosen[0] !== 0 || chosen[1] !== 0));
      element.dataset.hovered = String(!!label.object.userData.forceVisible);
      element.dataset.below = String((chosen?.[1] ?? 0) > 0);
      element.style.setProperty(
        '--label-leader',
        `${Math.min(22, Math.max(0, Math.abs(chosen?.[1] ?? 0) - 5))}px`
      );
      if (chosen) occupied.push({ x: x + chosen[0], y: y + chosen[1], w, h });
    }
    this.lastLayoutMs = performance.now() - layoutStart;
    this.renderer.render(scene, camera);
  }
  dispose() {
    this.resizeObserver?.disconnect();
    this.clear();
    this.renderer.domElement.remove();
  }
}
