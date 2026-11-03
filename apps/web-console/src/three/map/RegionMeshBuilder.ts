import {
  Group,
  Shape,
  Path,
  ExtrudeGeometry,
  ShapeGeometry,
  Mesh,
  BufferGeometry,
  Float32BufferAttribute,
  MathUtils,
} from 'three';
import { mergeGeometries } from 'three/addons/utils/BufferGeometryUtils.js';
import type { Feature, Polygon, MultiPolygon } from 'geojson';
import { project, projectCountry } from './GeoProjection';
import { regionOutline } from './RegionOutlineBuilder';
import { mapSurfaceMaterial } from '../materials/MapSurfaceMaterial';
import { mapSideMaterial } from '../materials/MapSideMaterial';
export const COUNTRY_DEPTH = 0.72;
export function bevelFor(depth: number, extent: number) {
  return {
    thickness: Math.min(MathUtils.clamp(depth * 0.08, 0.015, 0.055), extent * 0.02),
    size: Math.min(MathUtils.clamp(depth * 0.05, 0.008, 0.03), extent * 0.008),
  };
}
export function buildRegion(f: Feature, depth = COUNTRY_DEPTH) {
  const group = new Group();
  group.userData = {
    adcode: Number(f.properties?.adcode),
    name: f.properties?.name,
    lift: 0,
    base: 0,
    depth,
  };
  if (!f.geometry || !['Polygon', 'MultiPolygon'].includes(f.geometry.type)) return group;
  const geo = f.geometry as Polygon | MultiPolygon;
  const polygons = geo.type === 'Polygon' ? [geo.coordinates] : geo.coordinates;
  const projection = depth === COUNTRY_DEPTH ? projectCountry : project;
  const top = mapSurfaceMaterial(),
    side = mapSideMaterial();
  const tops: BufferGeometry[] = [];
  const buckets = [
    { position: [] as number[], normal: [] as number[], uv: [] as number[] },
    { position: [] as number[], normal: [] as number[], uv: [] as number[] },
  ];
  let maxBevel = 0;
  for (const polygon of polygons) {
    if (!polygon[0]?.length) continue;
    const make = (ring: number[][], s: Path) => {
      ring.forEach((p, i) => {
        const [x, z] = projection(p[0]!, p[1]!);
        if (i === 0) s.moveTo(x, -z);
        else s.lineTo(x, -z);
      });
      return s;
    };
    const shape = make(polygon[0], new Shape()) as Shape;
    for (const hole of polygon.slice(1)) shape.holes.push(make(hole, new Path()));
    const points = shape.getPoints(),
      xs = points.map((p) => p.x),
      ys = points.map((p) => p.y);
    const extent = Math.min(Math.max(...xs) - Math.min(...xs), Math.max(...ys) - Math.min(...ys));
    const wallDepth = Math.min(depth, 0.002 + extent * 0.28);
    const b = bevelFor(wallDepth, extent);
    maxBevel = Math.max(maxBevel, b.thickness);
    const geometry = new ExtrudeGeometry(shape, {
      depth: wallDepth,
      steps: 1,
      bevelEnabled: b.size > 0.0001,
      bevelThickness: b.thickness,
      bevelSize: b.size,
      bevelSegments: 1,
    });
    geometry.translate(0, 0, depth - wallDepth);
    const p = geometry.getAttribute('position'),
      n = geometry.getAttribute('normal'),
      uv = geometry.getAttribute('uv');
    for (const section of geometry.groups.filter((g) => g.materialIndex === 1)) {
      for (let i = section.start; i < section.start + section.count; i += 3) {
        const bucket = buckets[Math.abs(n.getZ(i)) > 0.02 ? 1 : 0]!;
        for (let j = i; j < i + 3; j++) {
          bucket.position.push(p.getX(j), p.getY(j), p.getZ(j));
          bucket.normal.push(n.getX(j), n.getY(j), n.getZ(j));
          bucket.uv.push(uv.getX(j), uv.getY(j));
        }
      }
    }
    geometry.dispose();
    tops.push(new ShapeGeometry(shape).translate(0, 0, depth + b.thickness + 0.001));
  }
  const geometries = buckets.map((bucket) => {
    const g = new BufferGeometry();
    for (const key of ['position', 'normal', 'uv'] as const)
      g.setAttribute(key, new Float32BufferAttribute(bucket[key], key === 'uv' ? 2 : 3));
    return g;
  });
  const wall = new Mesh(geometries[0], side),
    surface = new Mesh(mergeGeometries(tops), top);
  for (const part of tops) part.dispose();
  for (const mesh of [wall, surface]) mesh.rotation.x = -Math.PI / 2;
  wall.name = 'side-flow';
  surface.name = 'top-gradient';
  surface.userData.region = group;
  const height = depth + maxBevel;
  const border = regionOutline(geo, height + 0.008, 0x54818c, 0.23, projection),
    hover = regionOutline(geo, height + 0.02, 0x8fd5df, 0, projection),
    selected = regionOutline(geo, height + 0.025, 0x8fd5df, 0, projection);
  // The micro-bevel (an extrude edge) is omitted to keep the region draw-call
  // budget low; the side-flow wall already carries the extruded depth.
  group.add(wall, surface, border, hover, selected);
  Object.assign(group.userData, { top, hover, selected, feature: f, bevel: maxBevel });
  return group;
}

/**
 * Merges every province's decorative side-flow wall into a single mesh with a
 * single shared material. Walls carry no region pick/outline semantics, so this
 * collapses 34 draw calls to 1 without touching hover/selected/pick.
 */
export function mergeSideWalls(country: Group): Mesh | null {
  const walls: BufferGeometry[] = [];
  for (const region of country.children) {
    const wall = region.getObjectByName('side-flow') as Mesh | undefined;
    if (!wall || !wall.geometry) continue;
    wall.updateWorldMatrix(true, true);
    walls.push(wall.geometry.clone().applyMatrix4(wall.matrixWorld));
    wall.geometry.dispose();
    (wall.material as { dispose?: () => void }).dispose?.();
    wall.removeFromParent();
  }
  if (!walls.length) return null;
  const merged = mergeGeometries(walls);
  if (!merged) return null;
  for (const g of walls) g.dispose();
  const mesh = new Mesh(merged, mapSideMaterial());
  mesh.name = 'merged-side-flow';
  return mesh;
}
