import * as THREE from 'three';
import CameraControls from 'camera-controls';
import { CameraPresets, type CameraPresetName } from './CameraPresets';
CameraControls.install({ THREE });
/** CSS-px bands of the stage covered by HUD controls (measured from the DOM by the stage). */
export type SafeInsets = { top: number; right: number; bottom: number; left: number };
const REGION_FRAME = /^(COUNTRY|PROVINCE|CITY)_/;
export class CameraRig {
  /**
   * R5: region presets (country/province/city) are fitted into the stage's real
   * safe frame — canvas minus HUD bands minus the Inspector drawer — instead of a
   * fixed fraction of the canvas. Unset (no DOM), the preset fractions apply.
   */
  safeArea?: () => SafeInsets | undefined;
  /** Last safe frame used for a region fit, for diagnostics. */
  lastSafe: (SafeInsets & { inset: number }) | null = null;
  camera = new THREE.PerspectiveCamera(38, 1, 0.03, 1000);
  controls: CameraControls;
  preset: CameraPresetName = 'COUNTRY_OVERVIEW';
  countryMode: 'overview' | 'assets' = 'overview';
  observation: 'operations' | 'cinematic' = 'operations';
  private width = 1;
  private height = 1;
  private inspectorWidth = 0;
  setViewPadding(width: number) {
    this.inspectorWidth = width;
    this.resize(this.width, this.height);
  }
  constructor(element: HTMLElement) {
    this.controls = new CameraControls(this.camera, element);
    this.controls.smoothTime = 0.18;
    this.controls.minPolarAngle = 0.3;
    this.controls.maxPolarAngle = 1.24;
    this.controls.minDistance = 0.25;
    this.controls.maxDistance = 280;
    this.controls.restThreshold = 0.06;
    this.controls.mouseButtons.right = CameraControls.ACTION.TRUCK;
    this.controls.setLookAt(0, 65, 110, 0, 0, 0, false);
  }
  resize(width: number, height: number) {
    this.width = width;
    this.height = height;
    this.camera.aspect = width / height;
    this.camera.updateProjectionMatrix();
    // Keep the offset whenever enough uncovered stage remains (the homepage drawer
    // is 320 px over a 960 px stage at 1280×720; the old 1000 px floor disabled it).
    if (this.inspectorWidth && width - this.inspectorWidth >= 480)
      this.camera.setViewOffset(width, height, this.inspectorWidth / 2, 0, width, height);
    else this.camera.clearViewOffset();
  }
  async frame(
    box: THREE.Box3,
    preset: CameraPresetName,
    transition = true,
    weightedCenter?: THREE.Vector3,
    samples?: THREE.Vector3[]
  ) {
    if (this.observation === 'cinematic') {
      const modes: Partial<Record<CameraPresetName, CameraPresetName>> = {
        COUNTRY_OVERVIEW: 'COUNTRY_CINEMATIC',
        PROVINCE_CONTEXT: 'PROVINCE_CINEMATIC',
        CITY_CONTEXT: 'CITY_CINEMATIC',
        PROJECT_FOCUS: 'PROJECT_CINEMATIC',
        DEVICE_FOCUS: 'DEVICE_CINEMATIC',
      };
      preset = modes[preset] ?? preset;
    }
    this.preset = preset;
    const config = CameraPresets[preset];
    this.camera.fov = config.fov;
    this.camera.updateProjectionMatrix();
    const asset = preset.startsWith('COUNTRY_') && this.countryMode === 'assets';
    const center = box.getCenter(new THREE.Vector3());
    if (preset.startsWith('COUNTRY_')) center.z += box.getSize(new THREE.Vector3()).z * 0.09;
    const leanWorld = new THREE.Vector3();
    if (asset && weightedCenter) {
      const before = center.clone();
      center.lerp(weightedCenter, 0.08);
      leanWorld.subVectors(center, before);
    }
    const corners: THREE.Vector3[] = [];
    for (const x of [box.min.x, box.max.x])
      for (const y of [box.min.y, box.max.y])
        for (const z of [box.min.z, box.max.z]) corners.push(new THREE.Vector3(x, y, z));
    const oldPosition = this.controls.getPosition(new THREE.Vector3()),
      oldTarget = this.controls.getTarget(new THREE.Vector3());
    let targetWidth = config.width * (asset ? 1.13 : 1);
    let targetHeight = asset ? 0.995 : config.height;
    // Desired NDC centre of the projected region (0,0 = canvas centre).
    let desiredX = 0,
      desiredY = 0;
    const safe = REGION_FRAME.test(preset) ? this.safeArea?.() : undefined;
    this.lastSafe = null;
    if (safe) {
      // The drawer strip is excluded only while the projection offset compensates for it.
      const inset = this.camera.view?.enabled ? this.inspectorWidth : 0;
      const usableW = Math.max(1, this.width - inset - safe.left - safe.right);
      const usableH = Math.max(1, this.height - safe.top - safe.bottom);
      // Half-extent in NDC equals the fraction of the canvas the region may span.
      targetWidth = usableW / this.width;
      targetHeight = usableH / this.height;
      if (asset) {
        // 设备聚焦 keeps its existing 1.13× zoom over the overview fit, never past the canvas.
        targetWidth = Math.min(targetWidth * 1.13, 0.995);
        targetHeight = Math.min(targetHeight * 1.13, 0.995);
      }
      desiredX = (safe.left - safe.right - inset) / this.width;
      desiredY = (safe.bottom - safe.top) / this.height;
      this.lastSafe = { ...safe, inset };
    }
    const projected = new THREE.Vector3();
    const right = new THREE.Vector3();
    // 设备聚焦: NDC lean of the projected region caused by moving the centre toward
    // the weighted asset centre (bounded so the zoomed country stays on canvas).
    let lean = 0; // world units along the camera's right axis
    const measure = (distance: number) => {
      this.camera.position.set(
        center.x + distance * config.yaw,
        center.y + distance * Math.sin(config.elevation),
        center.z + distance * Math.cos(config.elevation)
      );
      this.camera.lookAt(center);
      this.camera.updateMatrixWorld();
      let minX = 1e9,
        minY = 1e9,
        maxX = -1e9,
        maxY = -1e9;
      for (const p of samples ?? corners) {
        projected.copy(p).project(this.camera);
        minX = Math.min(minX, projected.x);
        maxX = Math.max(maxX, projected.x);
        minY = Math.min(minY, projected.y);
        maxY = Math.max(maxY, projected.y);
      }
      return { width: (maxX - minX) / 2, height: (maxY - minY) / 2, minX, maxX, minY, maxY };
    };
    let distance = 100;
    for (let pass = 0; pass < 4; pass++) {
      let low = 0.3,
        high = 450;
      for (let i = 0; i < 40; i++) {
        const d = (low + high) / 2,
          bounds = measure(d);
        if (bounds.width > targetWidth || bounds.height > targetHeight) low = d;
        else high = d;
      }
      distance = high;
      const bounds = measure(distance),
        cy = (bounds.minY + bounds.maxY) / 2 - desiredY;
      const halfH = distance * Math.tan(THREE.MathUtils.degToRad(this.camera.fov / 2));
      const shift = cy * halfH;
      center.y += shift * Math.cos(config.elevation);
      center.z -= shift * Math.sin(config.elevation);
      if (safe) {
        // Horizontal re-centre into the safe frame (yaw and the drawer offset shift it).
        const cx = (bounds.minX + bounds.maxX) / 2 - desiredX;
        right.setFromMatrixColumn(this.camera.matrixWorld, 0);
        center.addScaledVector(right, cx * halfH * this.camera.aspect);
      }
    }
    if (safe && asset) {
      // 设备聚焦 keeps its deliberate lean toward the weighted asset centre after the
      // safe-frame centring, bounded so the zoomed country stays inside the canvas.
      right.setFromMatrixColumn(this.camera.matrixWorld, 0);
      const along = leanWorld.dot(right);
      const b = measure(distance);
      const halfW = distance * Math.tan(THREE.MathUtils.degToRad(this.camera.fov / 2)) * this.camera.aspect;
      const room = (along >= 0 ? 0.995 - b.maxX : b.minX + 0.995) * halfW;
      lean = Math.sign(along) * Math.min(Math.abs(along), Math.max(0, room));
      center.addScaledVector(right, lean);
    }
    this.controls.setLookAt(
      oldPosition.x,
      oldPosition.y,
      oldPosition.z,
      oldTarget.x,
      oldTarget.y,
      oldTarget.z,
      false
    );
    await this.controls.setLookAt(
      center.x + distance * config.yaw,
      center.y + distance * Math.sin(config.elevation),
      center.z + distance * Math.cos(config.elevation),
      center.x,
      center.y,
      center.z,
      transition
    );
  }
  get elevation() {
    return Math.asin(
      this.controls
        .getPosition(new THREE.Vector3())
        .sub(this.controls.getTarget(new THREE.Vector3()))
        .normalize().y
    );
  }
  get viewport() {
    return { width: this.width, height: this.height };
  }
  dispose() {
    this.controls.dispose();
  }
}
