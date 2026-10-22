import { Mesh, Line, Points, type Object3D, type Material } from 'three';
import { CSS2DObject } from 'three/addons/renderers/CSS2DRenderer.js';
export function disposeObject(root: Object3D) {
  const materials = new Set<Material>();
  const geometries = new Set();
  root.traverse((o) => {
    if (o instanceof Mesh || o instanceof Line || o instanceof Points) {
      if (!o.geometry.userData.shared) geometries.add(o.geometry);
      for (const m of Array.isArray(o.material) ? o.material : [o.material]) materials.add(m);
    }
    if (o instanceof CSS2DObject) o.element.remove();
  });
  for (const g of geometries as Set<import('three').BufferGeometry>) g.dispose();
  for (const m of materials) m.dispose();
  root.removeFromParent();
}
