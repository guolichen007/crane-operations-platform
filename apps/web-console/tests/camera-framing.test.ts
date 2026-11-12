// R5-UX-R1 source-level checks for CameraRig safe-frame fitting (no DOM: camera-controls
// gets a minimal element stub). The projected region must fill the safe frame in its
// binding dimension and be centred in it; without a safe frame the old fixed preset
// fraction still applies (fallback).
import { describe, it, expect, vi } from 'vitest';
import { Box3, Vector3 } from 'three';

vi.mock('camera-controls', () => {
  class Controls {
    static install() {}
    static ACTION = { TRUCK: 2 };
    mouseButtons = { right: 0 };
    smoothTime = 0;
    minPolarAngle = 0;
    maxPolarAngle = 0;
    minDistance = 0;
    maxDistance = 0;
    restThreshold = 0;
    pos = new Vector3();
    tgt = new Vector3();
    constructor(public camera: import('three').PerspectiveCamera) {}
    setLookAt(px: number, py: number, pz: number, tx: number, ty: number, tz: number) {
      this.pos.set(px, py, pz);
      this.tgt.set(tx, ty, tz);
      this.camera.position.copy(this.pos);
      this.camera.lookAt(this.tgt);
      this.camera.updateMatrixWorld();
      return Promise.resolve();
    }
    getPosition(v: Vector3) {
      return v.copy(this.pos);
    }
    getTarget(v: Vector3) {
      return v.copy(this.tgt);
    }
    dispose() {}
  }
  return { default: Controls };
});

const { CameraRig } = await import('../src/three/core/CameraRig');

/** A flat, wide region (like 广东) sampled on its outline. */
function region(w: number, d: number) {
  const pts: Vector3[] = [];
  for (let i = 0; i <= 40; i++) {
    const t = i / 40;
    pts.push(new Vector3(-w / 2 + w * t, 1, -d / 2), new Vector3(-w / 2 + w * t, 1, d / 2));
    pts.push(new Vector3(-w / 2, 1, -d / 2 + d * t), new Vector3(w / 2, 1, -d / 2 + d * t));
  }
  return pts;
}
function projected(rig: InstanceType<typeof CameraRig>, pts: Vector3[], W: number, H: number) {
  let x0 = Infinity, x1 = -Infinity, y0 = Infinity, y1 = -Infinity;
  for (const p of pts) {
    const v = p.clone().project(rig.camera);
    const x = ((v.x + 1) * W) / 2, y = ((1 - v.y) * H) / 2;
    x0 = Math.min(x0, x); x1 = Math.max(x1, x); y0 = Math.min(y0, y); y1 = Math.max(y1, y);
  }
  return { x0, x1, y0, y1 };
}

describe('CameraRig 安全取景框适配（R5-UX-R1 B）', () => {
  for (const [W, H, w, d] of [
    [1058, 852, 20, 8], // wide region, landscape stage → width binds
    [851, 958, 6, 6], // compact region, portrait stage
    [1698, 978, 4, 9], // tall region, wide stage → height binds
  ] as const)
    it(`${W}×${H} 区域 ${w}×${d}：绑定方向充满安全框并居中`, async () => {
      const rig = new CameraRig({} as HTMLElement);
      rig.resize(W, H);
      const safe = { top: 100, right: 60, bottom: 80, left: 60 };
      rig.safeArea = () => safe;
      const pts = region(w, d);
      await rig.frame(new Box3().setFromPoints(pts), 'PROVINCE_CONTEXT', false, undefined, pts);
      const b = projected(rig, pts, W, H);
      const usableW = W - safe.left - safe.right, usableH = H - safe.top - safe.bottom;
      const fillW = (b.x1 - b.x0) / usableW, fillH = (b.y1 - b.y0) / usableH;
      expect(Math.max(fillW, fillH)).toBeGreaterThan(0.97);
      expect(Math.max(fillW, fillH)).toBeLessThanOrEqual(1.001);
      expect(b.x0).toBeGreaterThanOrEqual(safe.left - 1);
      expect(b.x1).toBeLessThanOrEqual(W - safe.right + 1);
      expect(b.y0).toBeGreaterThanOrEqual(safe.top - 1);
      expect(b.y1).toBeLessThanOrEqual(H - safe.bottom + 1);
      expect(Math.abs(b.x0 - safe.left - (W - safe.right - b.x1))).toBeLessThan(2);
      expect(rig.lastSafe).toMatchObject({ ...safe, inset: 0 });
    });

  it('打开抽屉（视口偏移）时区域落在未遮挡部分并居中', async () => {
    const W = 1044, H = 540, inspector = 320;
    const rig = new CameraRig({} as HTMLElement);
    rig.resize(W, H);
    rig.setViewPadding(inspector);
    const safe = { top: 60, right: 40, bottom: 40, left: 40 };
    rig.safeArea = () => safe;
    const pts = region(10, 6);
    await rig.frame(new Box3().setFromPoints(pts), 'PROVINCE_CONTEXT', false, undefined, pts);
    const b = projected(rig, pts, W, H);
    expect(b.x1).toBeLessThanOrEqual(W - inspector - safe.right + 1);
    expect(Math.abs(b.x0 - safe.left - (W - inspector - safe.right - b.x1))).toBeLessThan(2);
  });

  it('没有安全框（无 DOM）时退回预设比例，不强行放大', async () => {
    const W = 1058, H = 852;
    const rig = new CameraRig({} as HTMLElement);
    rig.resize(W, H);
    const pts = region(20, 8);
    await rig.frame(new Box3().setFromPoints(pts), 'COUNTRY_OVERVIEW', false, undefined, pts);
    const b = projected(rig, pts, W, H);
    expect((b.x1 - b.x0) / W).toBeCloseTo(0.62, 2);
    expect(rig.lastSafe).toBeNull();
  });

  it('省级运营视角俯角大于立体视角（地市边界更清晰）', async () => {
    const { CameraPresets } = await import('../src/three/core/CameraPresets');
    expect(CameraPresets.PROVINCE_CONTEXT.elevation).toBeGreaterThan(CameraPresets.PROVINCE_CINEMATIC.elevation);
    expect(CameraPresets.CITY_CONTEXT.elevation).toBeGreaterThan(CameraPresets.CITY_CINEMATIC.elevation);
    expect(CameraPresets.PROVINCE_CONTEXT.elevation).toBeLessThan(Math.PI / 2);
  });
});
