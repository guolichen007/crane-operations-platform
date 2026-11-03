import { Raycaster, Vector2, type Camera, type Group, type Object3D } from 'three';
import { ScreenSpacePicker, type AssetPick, type PickTarget } from './ScreenSpacePicker';
export type InteractionPick = AssetPick | { kind: 'region'; id: string; distancePx: number };
export class InteractionRouter {
  private ray = new Raycaster();
  private pointer = new Vector2();
  private down = { x: 0, y: 0 };
  private dragging = false;
  private rafPending = false;
  private queuedMove: PointerEvent | null = null;
  readonly picker: ScreenSpacePicker;
  lastPick: (InteractionPick & { regionBehind?: number }) | null = null;
  constructor(
    private element: HTMLElement,
    private camera: Camera,
    private regions: () => Object3D[],
    targets: () => PickTarget[],
    private hover: (region: Group | null) => void,
    private select: (target: InteractionPick) => void,
    private busy: () => boolean,
    private hoverAsset?: (target: AssetPick | null) => void,
    private onDragChange?: (dragging: boolean) => void
  ) {
    this.picker = new ScreenSpacePicker(element, camera, targets);
    element.addEventListener('pointermove', this.move);
    element.addEventListener('pointerleave', this.leave);
    element.addEventListener('pointerdown', this.start);
    element.addEventListener('pointerup', this.end);
  }
  get isDragging() {
    return this.dragging;
  }
  private setDragging(value: boolean) {
    if (this.dragging === value) return;
    this.dragging = value;
    this.onDragChange?.(value);
    if (value) {
      // Entering drag: drop hover state and any pending hover pick.
      this.queuedMove = null;
      this.hover(null);
      this.hoverAsset?.(null);
    }
  }
  private region(e: PointerEvent) {
    const r = this.element.getBoundingClientRect();
    this.pointer.set(
      ((e.clientX - r.left) / r.width) * 2 - 1,
      1 - ((e.clientY - r.top) / r.height) * 2
    );
    this.ray.setFromCamera(this.pointer, this.camera);
    return this.ray.intersectObjects(this.regions(), true).find((h) => h.object.userData.region)
      ?.object.userData.region as Group | undefined;
  }
  /** pointermove is rAF-throttled: at most one pick per frame. */
  private move = (e: PointerEvent) => {
    if (e.buttons && Math.hypot(e.clientX - this.down.x, e.clientY - this.down.y) > 5)
      this.setDragging(true);
    if (this.rafPending) {
      this.queuedMove = e;
      return;
    }
    this.rafPending = true;
    const process = () => {
      this.rafPending = false;
      const event = this.queuedMove ?? null;
      this.queuedMove = null;
      if (event) this.processMove(event);
    };
    if (typeof requestAnimationFrame === 'function') requestAnimationFrame(process);
    else process();
  };
  private processMove(e: PointerEvent) {
    if (this.busy() || this.dragging) return;
    const asset = this.picker.pick(e.clientX, e.clientY);
    this.hoverAsset?.(asset);
    this.hover(asset ? null : (this.region(e) ?? null));
    if (asset) this.element.style.cursor = 'pointer';
    else this.element.style.cursor = 'grab';
  }
  private leave = () => {
    this.hover(null);
    this.hoverAsset?.(null);
  };
  private start = (e: PointerEvent) => {
    this.down = { x: e.clientX, y: e.clientY };
  };
  private end = (e: PointerEvent) => {
    const wasDragging = this.dragging;
    this.setDragging(false);
    if (
      wasDragging ||
      this.busy() ||
      e.button !== 0 ||
      Math.hypot(e.clientX - this.down.x, e.clientY - this.down.y) > 5
    )
      return;
    const asset = this.picker.pick(e.clientX, e.clientY);
    const region = this.region(e);
    const result =
      asset ??
      (region
        ? { kind: 'region' as const, id: String(region.userData.adcode), distancePx: 0 }
        : null);
    if (!result) return;
    this.lastPick = { ...result, regionBehind: region?.userData.adcode };
    this.hover(null);
    this.select(result);
  };
  dispose() {
    this.element.removeEventListener('pointermove', this.move);
    this.element.removeEventListener('pointerleave', this.leave);
    this.element.removeEventListener('pointerdown', this.start);
    this.element.removeEventListener('pointerup', this.end);
  }
}
