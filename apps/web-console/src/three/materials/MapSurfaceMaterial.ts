import { Color, DoubleSide, MeshStandardMaterial } from 'three';
// Authored world-space gradient. No reference texture is shipped.
export function mapSurfaceMaterial() {
  const material = new MeshStandardMaterial({
    color: 0xffffff,
    roughness: 0.82,
    metalness: 0.22,
    emissive: 0x153958,
    emissiveIntensity: 1.1,
    transparent: true,
    side: DoubleSide,
  });
  material.onBeforeCompile = (shader) => {
    shader.uniforms.uMapEdge = { value: new Color('#234d71') };
    shader.uniforms.uMapCenter = { value: new Color('#386e96') };
    shader.vertexShader = 'varying vec3 vMapWorld;\n' + shader.vertexShader;
    shader.vertexShader = shader.vertexShader.replace(
      '#include <begin_vertex>',
      '#include <begin_vertex>\nvMapWorld = (modelMatrix * vec4(position, 1.0)).xyz;'
    );
    shader.fragmentShader =
      'varying vec3 vMapWorld;\nuniform vec3 uMapEdge;\nuniform vec3 uMapCenter;\n' +
      shader.fragmentShader;
    shader.fragmentShader = shader.fragmentShader.replace(
      '#include <color_fragment>',
      `#include <color_fragment>
       float field = clamp(0.78 - length(vMapWorld.xz / vec2(68.0, 55.0)) * 0.54
         + 0.08 * sin(vMapWorld.x * 0.055 + vMapWorld.z * 0.04), 0.0, 1.0);
       // Smooth multi-frequency grain: a 3% material variation without pixel noise.
       float grain = sin(vMapWorld.x * 2.7 + sin(vMapWorld.z * 1.8))
         * sin(vMapWorld.z * 2.2 + sin(vMapWorld.x * 1.2));
       float brushed = sin(vMapWorld.x * 0.7 + vMapWorld.z * 5.0) * 0.004;
       diffuseColor.rgb *= mix(uMapEdge, uMapCenter, field) * (1.0 + grain * 0.03 + brushed);`
    );
  };
  material.customProgramCacheKey = () => 'map-surface-v033';
  return material;
}
