import { Mesh, Line, Points, InstancedMesh, Texture, type Object3D, type Material } from 'three';
import { CSS2DObject } from 'three/addons/renderers/CSS2DRenderer.js';
export function disposeObject(root: Object3D) {
  const materials = new Set<Material>();
  const geometries = new Set();
  const textures = new Set<Texture>();
  root.traverse((o) => {
    if (o instanceof Mesh || o instanceof Line || o instanceof Points) {
      if (!o.geometry.userData.shared) geometries.add(o.geometry);
      for (const m of Array.isArray(o.material) ? o.material : [o.material]) materials.add(m);
    }
    if (o instanceof InstancedMesh) o.dispose();
    if (o instanceof CSS2DObject) o.element.remove();
  });
  for (const g of geometries as Set<import('three').BufferGeometry>) g.dispose();
  for (const m of materials) {
    for (const value of Object.values(m))
      if (value instanceof Texture && !value.userData.shared) textures.add(value);
    m.dispose();
  }
  for (const texture of textures) texture.dispose();
  root.removeFromParent();
}
