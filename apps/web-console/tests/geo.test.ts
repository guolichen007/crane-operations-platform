import { describe, it, expect } from 'vitest';
import { readFileSync, existsSync } from 'node:fs';
import { resolve } from 'node:path';
import { realProjects } from '../src/data';
import type { RegionEntry } from '../src/three/map/GeoJsonLoader';
const base = resolve('public/geo');
const manifest = JSON.parse(readFileSync(resolve(base, 'geo-manifest.json'), 'utf8')) as Record<
  string,
  RegionEntry
>;
describe('离线地图清单', () => {
  it('覆盖匿名登记项目的省市县路径及省直管县', () => {
    for (const p of realProjects) {
      let parent = 100000;
      for (const code of p.adminPath) {
        expect(manifest[code], p.projectName).toBeDefined();
        expect(manifest[code]!.parent, p.projectName).toBe(parent);
        parent = code;
      }
    }
  });
  it('所有清单资源已本地缓存，父级存在且不形成环', () => {
    for (const entry of Object.values(manifest)) {
      for (const path of [entry.childrenFile, entry.boundaryFile])
        if (path) expect(existsSync(resolve(base, path)), path).toBe(true);
      const seen = new Set<number>();
      let current: RegionEntry | undefined = entry;
      while (current) {
        expect(seen.has(current.adcode)).toBe(false);
        seen.add(current.adcode);
        current = current.parent ? manifest[current.parent] : undefined;
      }
    }
  });
});
