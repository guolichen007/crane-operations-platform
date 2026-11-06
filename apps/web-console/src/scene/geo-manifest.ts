// Single cached read of the authorized administrative-division manifest
// (public/geo/geo-manifest.json, DataV.GeoAtlas — internal prototype data, see
// docs/04-前端与可视化/地图数据合规说明.md). Shared by the scene provider (admin
// paths), the region deep link and the lightweight 2D map, so all three agree on
// one hierarchy and the file is fetched once.
import type { RegionManifestEntry } from './region-path';

export type GeoManifestEntry = RegionManifestEntry & {
  name: string;
  center: [number, number] | null;
  childrenFile: string | null;
  boundaryFile: string | null;
};
export type GeoManifest = Record<string, GeoManifestEntry>;

let cached: Promise<GeoManifest> | undefined;

export function loadGeoManifest(): Promise<GeoManifest> {
  if (!cached)
    cached = fetch(`${import.meta.env.BASE_URL}geo/geo-manifest.json`)
      .then((res) => {
        if (!res.ok) throw new Error('地图清单加载失败');
        return res.json() as Promise<GeoManifest>;
      })
      .catch((e) => {
        cached = undefined; // allow a retry
        throw e;
      });
  return cached;
}

/** Entries whose parent is `code` (the next drill level), in manifest order. */
export function childEntries(manifest: GeoManifest, code: number): GeoManifestEntry[] {
  return Object.values(manifest).filter((r) => r.parent === code);
}
