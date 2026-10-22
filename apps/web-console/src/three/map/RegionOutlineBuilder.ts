import { BufferGeometry, LineBasicMaterial, LineSegments, Vector3, Group } from 'three';
import type { Polygon, MultiPolygon } from 'geojson';
import { project } from './GeoProjection';
export function regionOutline(
  geometry: Polygon | MultiPolygon,
  height: number,
  color: number,
  opacity: number,
  projection = project
) {
  const group = new Group();
  const polygons = geometry.type === 'Polygon' ? [geometry.coordinates] : geometry.coordinates;
  const material = new LineBasicMaterial({ color, transparent: true, opacity, depthWrite: false });
  const points: Vector3[] = [];
  for (const polygon of polygons)
    for (const ring of polygon) {
      for (let i = 0; i < ring.length - 1; i++) {
        for (const p of [ring[i]!, ring[i + 1]!]) {
          const [x, z] = projection(p[0]!, p[1]!);
          points.push(new Vector3(x, height, z));
        }
      }
    }
  group.add(new LineSegments(new BufferGeometry().setFromPoints(points), material));
  group.visible = opacity > 0;
  return group;
}
