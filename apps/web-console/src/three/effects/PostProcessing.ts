import { HalfFloatType, NoToneMapping, type WebGLRenderer, type Scene, type Camera, type Object3D } from 'three';
import { EffectComposer, RenderPass, EffectPass, SelectiveBloomEffect, OutlineEffect, SMAAEffect, SMAAPreset, ToneMappingEffect, ToneMappingMode, VignetteEffect, BlendFunction } from 'postprocessing';
import type { QualityTier } from '../core/QualityManager';
export function createPostProcessing(renderer: WebGLRenderer, scene: Scene, camera: Camera) {
  // Only the final pass tone maps. Avoid renderer + effect double mapping.
  renderer.toneMapping = NoToneMapping;
  const composer = new EffectComposer(renderer, { frameBufferType: HalfFloatType, multisampling: 0 });
  const bloom = new SelectiveBloomEffect(scene, camera, { intensity: .3, luminanceThreshold: .45, mipmapBlur: true });
  bloom.ignoreBackground = true;
  const selected = new OutlineEffect(scene, camera, { visibleEdgeColor: 0x31d6e8, edgeStrength: 1.5, xRay: false });
  const hover = new OutlineEffect(scene, camera, { visibleEdgeColor: 0x2c8fff, edgeStrength: .8, xRay: false });
  const alarm = new OutlineEffect(scene, camera, { visibleEdgeColor: 0xff5d6c, edgeStrength: 1.2, xRay: false, pulseSpeed: 0 });
  // Selection layers must be distinct (default is layer 11).
  bloom.selection.layer = 11; selected.selection.layer = 12; hover.selection.layer = 13; alarm.selection.layer = 14;
  composer.addPass(new RenderPass(scene, camera));
  const bloomPass = new EffectPass(camera, bloom);
  const outlinePass = new EffectPass(camera, selected, hover, alarm);
  composer.addPass(bloomPass); composer.addPass(outlinePass);
  composer.addPass(new EffectPass(camera, new SMAAEffect({ preset: SMAAPreset.MEDIUM }), new VignetteEffect({ offset: .25, darkness: .035, blendFunction: BlendFunction.MULTIPLY })));
  composer.addPass(new EffectPass(camera, new ToneMappingEffect({ mode: ToneMappingMode.ACES_FILMIC })));
  return Object.assign(composer, {
    setQuality(tier: QualityTier) { bloom.intensity = tier === 'premium' ? .3 : .18; bloomPass.enabled = tier !== 'reduced'; outlinePass.enabled = tier !== 'reduced'; },
    setHighlights(objects: { bloom?: Object3D[]; selected?: Object3D[]; hover?: Object3D[]; alarm?: Object3D[] }) {
      bloom.selection.set(objects.bloom ?? []); selected.selection.set(objects.selected ?? []); hover.selection.set(objects.hover ?? []); alarm.selection.set(objects.alarm ?? []);
    },
  });
}
