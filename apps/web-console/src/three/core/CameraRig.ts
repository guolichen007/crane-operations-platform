import * as THREE from 'three';
import CameraControls from 'camera-controls';
import { CameraPresets, type CameraPresetName } from './CameraPresets';
CameraControls.install({ THREE });
export class CameraRig {
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
    if (this.inspectorWidth && width > 1000)
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
    if (asset && weightedCenter) center.lerp(weightedCenter, 0.08);
    const corners: THREE.Vector3[] = [];
    for (const x of [box.min.x, box.max.x])
      for (const y of [box.min.y, box.max.y])
        for (const z of [box.min.z, box.max.z]) corners.push(new THREE.Vector3(x, y, z));
    const oldPosition = this.controls.getPosition(new THREE.Vector3()),
      oldTarget = this.controls.getTarget(new THREE.Vector3());
    const targetWidth = config.width * (asset ? 1.13 : 1);
    const targetHeight = asset ? 0.995 : config.height;
    const projected = new THREE.Vector3();
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
        cy = (bounds.minY + bounds.maxY) / 2;
      const shift = cy * distance * Math.tan(THREE.MathUtils.degToRad(this.camera.fov / 2));
      center.y += shift * Math.cos(config.elevation);
      center.z -= shift * Math.sin(config.elevation);
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
