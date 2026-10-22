import { Color, MeshStandardMaterial } from 'three';
export const sceneTime = { value: 0 };
export function mapSideMaterial() {
  const material = new MeshStandardMaterial({
    color: 0xffffff,
    roughness: 0.9,
    metalness: 0.15,
    emissive: 0x0b2d38,
    emissiveIntensity: 0.65,
    transparent: true,
  });
  material.onBeforeCompile = (shader) => {
    shader.uniforms.uSceneTime = sceneTime;
    shader.uniforms.uSideLow = { value: new Color('#071c25') };
    shader.uniforms.uSideHigh = { value: new Color('#102d36') };
    shader.vertexShader = 'varying vec3 vSidePosition;\n' + shader.vertexShader;
    shader.vertexShader = shader.vertexShader.replace(
      '#include <begin_vertex>',
      '#include <begin_vertex>\nvSidePosition = position;'
    );
    shader.fragmentShader =
      'varying vec3 vSidePosition;\nuniform float uSceneTime;\nuniform vec3 uSideLow;\nuniform vec3 uSideHigh;\n' +
      shader.fragmentShader;
    shader.fragmentShader = shader.fragmentShader.replace(
      '#include <color_fragment>',
      `#include <color_fragment>
       diffuseColor.rgb *= mix(uSideLow, uSideHigh, clamp(vSidePosition.z / 0.72, 0.0, 1.0));`
    );
    shader.fragmentShader = shader.fragmentShader.replace(
      '#include <emissivemap_fragment>',
      `#include <emissivemap_fragment>
       float scan = pow(0.5 + 0.5 * sin(vSidePosition.z * 11.0 - uSceneTime * 0.57), 5.0);
       float stripe = 0.5 + 0.5 * sin(vSidePosition.x * 16.0 + vSidePosition.y * 12.0);
       totalEmissiveRadiance += vec3(0.016, 0.042, 0.049) * (0.32 + scan * 0.68) * (0.7 + stripe * 0.3);`
    );
  };
  material.customProgramCacheKey = () => 'map-side-v033';
  return material;
}
