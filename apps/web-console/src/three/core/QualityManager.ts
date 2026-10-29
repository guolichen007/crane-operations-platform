import type { WebGLRenderer } from 'three';
export type QualityTier = 'premium' | 'balanced' | 'reduced';
export const qualityProfiles = {
  premium: { dpr: 1.5, minFPS: 45, shadows: true, bloom: 0.3 },
  balanced: { dpr: 1.25, minFPS: 40, shadows: false, bloom: 0.18 },
  reduced: { dpr: 1, minFPS: 30, shadows: false, bloom: 0.08 },
} as const;
export class QualityManager {
  tier: QualityTier;
  private elapsed = 0;
  private frames = 0;
  private slowWindows = 0;
  constructor(
    readonly dpr: number,
    readonly reducedMotion = false,
    maxTextureSize = 8192,
    rendererName = ''
  ) {
    this.tier =
      reducedMotion ||
      maxTextureSize < 4096 ||
      /swiftshader|llvmpipe|softpipe|software|microsoft basic render/i.test(rendererName)
        ? 'reduced'
        : dpr > 2 || maxTextureSize < 8192
          ? 'balanced'
          : 'premium';
  }
  get profile() {
    return qualityProfiles[this.tier];
  }
  get pixelRatio() {
    return Math.min(this.dpr, this.profile.dpr);
  }
  sample(dt: number) {
    if (dt <= 0 || dt > 0.5) return false;
    this.elapsed += dt;
    this.frames++;
    if (this.elapsed < 2) return false;
    const fps = this.frames / this.elapsed;
    this.slowWindows = fps < this.profile.minFPS ? this.slowWindows + 1 : 0;
    this.elapsed = 0;
    this.frames = 0;
    if (this.slowWindows < 3 || this.tier === 'reduced') return false;
    this.tier = this.tier === 'premium' ? 'balanced' : 'reduced';
    this.slowWindows = 0;
    return true;
  }
}
export function rendererQuality(renderer: WebGLRenderer, dpr: number, reducedMotion = false) {
  let name = '';
  try {
    const gl = renderer.getContext(),
      debug = gl.getExtension('WEBGL_debug_renderer_info');
    name = String(gl.getParameter(debug ? debug.UNMASKED_RENDERER_WEBGL : gl.RENDERER));
  } catch {
    /* Capability probing may be restricted; continuous FPS still controls quality. */
  }
  return new QualityManager(dpr, reducedMotion, renderer.capabilities.maxTextureSize, name);
}
