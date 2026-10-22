import { Group, Mesh, MeshBasicMaterial, AdditiveBlending } from 'three';
import { mergeGeometries } from 'three/addons/utils/BufferGeometryUtils.js';
import { LineSegments2 } from 'three/addons/lines/LineSegments2.js';
import { LineSegmentsGeometry } from 'three/addons/lines/LineSegmentsGeometry.js';
import { LineMaterial } from 'three/addons/lines/LineMaterial.js';
import type { Polygon, MultiPolygon } from 'geojson';
import { projectCountry } from '../map/GeoProjection';
export class CountryOuterGlow {
  group = new Group();
  private lines: LineMaterial[] = [];
  constructor(country: Group) {
    this.group.name = 'China contour ghost silhouette';
    const tops = country.children.map((r) => {
      const top = r.children[1] as Mesh;
      const g = top.geometry.clone();
      g.rotateX(-Math.PI / 2);
      const p = g.getAttribute('position');
      for (let i = 0; i < p.count; i++) p.setY(i, 0);
      return g;
    });
    const base = new Mesh(
      mergeGeometries(tops),
      new MeshBasicMaterial({
        color: '#0a303b',
        transparent: true,
        opacity: 0.32,
        depthWrite: false,
      })
    );
    base.name = 'country-underlay';
    base.scale.set(1.008, 1, 1.008);
    base.position.y = -0.15;
    const lower = new Mesh(
      base.geometry,
      new MeshBasicMaterial({
        color: '#164754',
        transparent: true,
        opacity: 0.11,
        depthWrite: false,
      })
    );
    lower.name = 'country-underlay-lower';
    lower.scale.set(1.018, 1, 1.018);
    lower.position.y = -0.24;
    this.group.add(lower, base);
    tops.forEach((g) => g.dispose());
    // Shared edges cancel; coastlines and exterior border retain their cached coordinates.
    const edges = new Map<string, { count: number; a: number[]; b: number[] }>();
    const key = (p: number[]) => `${p[0]!.toFixed(5)},${p[1]!.toFixed(5)}`;
    for (const r of country.children) {
      const geo = r.userData.feature.geometry as Polygon | MultiPolygon;
      const polys = geo.type === 'Polygon' ? [geo.coordinates] : geo.coordinates;
      for (const poly of polys)
        for (const ring of poly)
          for (let i = 0; i < ring.length - 1; i++) {
            const a = ring[i]!,
              b = ring[i + 1]!,
              ak = key(a),
              bk = key(b);
            const id = ak < bk ? `${ak}|${bk}` : `${bk}|${ak}`;
            const edge = edges.get(id);
            if (edge) edge.count++;
            else edges.set(id, { count: 1, a, b });
          }
    }
    const positions: number[] = [];
    for (const e of edges.values())
      if (e.count === 1)
        for (const p of [e.a, e.b]) {
          const [x, z] = projectCountry(p[0]!, p[1]!);
          positions.push(x, 0.79, z);
        }
    const geometry = new LineSegmentsGeometry().setPositions(positions);
    for (const [width, opacity, name] of [
      [1.1, 0.68, 'outer-border-solid'],
      [3.5, 0.06, 'outer-border-glow'],
    ] as const) {
      const mat = new LineMaterial({
        color: 0x54818c,
        linewidth: width,
        transparent: true,
        opacity,
        depthWrite: false,
        ...(name.endsWith('glow') ? { blending: AdditiveBlending } : {}),
      });
      this.lines.push(mat);
      const line = new LineSegments2(geometry, mat);
      line.name = name;
      this.group.add(line);
    }
  }
  resize(width: number, height: number) {
    for (const m of this.lines) m.resolution.set(width, height);
  }
}
