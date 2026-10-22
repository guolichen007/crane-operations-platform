import { Scene, AmbientLight, DirectionalLight, HemisphereLight } from 'three';
export function addAtmosphere(scene: Scene) {
  scene.add(new AmbientLight(0xb1c9cc, 0.8), new HemisphereLight(0xc7e8e8, 0x142731, 0.85));
  const key = new DirectionalLight(0xe3f0ea, 2.8);
  key.position.set(-35, 65, 35);
  const rim = new DirectionalLight(0x9bd9dc, 1.1);
  rim.position.set(30, 20, -28);
  const warm = new DirectionalLight(0xffdfae, 0.24);
  warm.position.set(-10, 8, 25);
  scene.add(key, rim, warm);
}
