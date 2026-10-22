import type { WebGLRenderer, Scene, Camera } from 'three';
import {
  EffectComposer,
  RenderPass,
  EffectPass,
  BloomEffect,
  VignetteEffect,
  BlendFunction,
} from 'postprocessing';
export function createPostProcessing(renderer: WebGLRenderer, scene: Scene, camera: Camera) {
  const composer = new EffectComposer(renderer, {
    multisampling: Math.min(4, renderer.capabilities.maxSamples),
  });
  composer.addPass(new RenderPass(scene, camera));
  composer.addPass(
    new EffectPass(
      camera,
      new BloomEffect({ intensity: 0.36, luminanceThreshold: 0.86, mipmapBlur: true }),
      new VignetteEffect({ offset: 0.25, darkness: 0.035, blendFunction: BlendFunction.MULTIPLY })
    )
  );
  return composer;
}
