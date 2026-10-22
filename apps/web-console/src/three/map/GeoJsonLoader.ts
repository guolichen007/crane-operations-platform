import type { Feature, FeatureCollection } from 'geojson';
import { ResourceManager } from '../core/ResourceManager';
import type { MapLevel } from './DrilldownController';
export type RegionEntry = MapLevel & {
  parent: number | null;
  center: [number, number];
  childrenFile: string | null;
  boundaryFile: string | null;
};
export class GeoJsonLoader {
  manifest: Record<string, RegionEntry> = {};
  private cache = new ResourceManager<FeatureCollection>(16);
  async init() {
    const response = await fetch(`${import.meta.env.BASE_URL}geo/geo-manifest.json`);
    if (!response.ok) throw Error('地图清单加载失败');
    this.manifest = await response.json();
  }
  async file(path: string) {
    return this.cache.get(path, async () => {
      const response = await fetch(`${import.meta.env.BASE_URL}geo/${path}`);
      if (!response.ok) throw Error('本地地图资源缺失：' + path);
      return response.json();
    });
  }
  async children(code: number): Promise<FeatureCollection> {
    const r = this.manifest[code];
    if (!r) throw Error('行政区数据不存在');
    if (r.childrenFile) return this.file(r.childrenFile);
    const all = await this.file(r.boundaryFile!);
    return {
      type: 'FeatureCollection',
      features: all.features.filter((f) => Number(f.properties?.adcode) === code),
    };
  }
  path(code: number) {
    const result: RegionEntry[] = [];
    let r = this.manifest[code];
    while (r && r.adcode !== 100000) {
      result.unshift(r);
      r = this.manifest[r.parent!];
    }
    return result;
  }
  childrenEntries(code: number) {
    return Object.values(this.manifest).filter((r) => r.parent === code);
  }
  boundary(feature: Feature) {
    return feature;
  }
  dispose() {
    this.cache.clear();
  }
}
