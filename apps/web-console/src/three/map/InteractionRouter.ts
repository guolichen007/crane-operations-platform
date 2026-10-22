import { Raycaster, Vector2, type Camera, type Group, type Object3D } from 'three';
import { ScreenSpacePicker, type AssetPick, type PickTarget } from './ScreenSpacePicker';
export type InteractionPick = AssetPick | { kind: 'region'; id: string; distancePx: number };
export class InteractionRouter {
  private ray = new Raycaster();
  private pointer = new Vector2();
  private down = { x: 0, y: 0 };
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
    private hoverAsset?: (target: AssetPick | null) => void
  ) {
    this.picker = new ScreenSpacePicker(element, camera, targets);
    element.addEventListener('pointermove', this.move);
    element.addEventListener('pointerleave', this.leave);
    element.addEventListener('pointerdown', this.start);
    element.addEventListener('pointerup', this.end);
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
  private move = (e: PointerEvent) => {
    if (this.busy()) return;
    const asset = this.picker.pick(e.clientX, e.clientY);
    this.hoverAsset?.(asset);
    this.hover(asset ? null : (this.region(e) ?? null));
    if (asset) this.element.style.cursor = 'pointer';
  };
  private leave = () => {
    this.hover(null);
    this.hoverAsset?.(null);
  };
  private start = (e: PointerEvent) => {
    this.down = { x: e.clientX, y: e.clientY };
  };
  private end = (e: PointerEvent) => {
    if (
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
