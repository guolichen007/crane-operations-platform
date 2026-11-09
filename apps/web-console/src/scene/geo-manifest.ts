// Single cached read of the authorized administrative-division manifest
// (public/geo/geo-manifest.json, DataV.GeoAtlas — internal prototype data, see
// docs/04-前端与可视化/地图数据合规说明.md). Shared by the scene provider (admin
// paths), the region deep link and the lightweight 2D map, so all three agree on
// one hierarchy and the file is fetched once.
//
// H2-1D: the same read also yields the manifest VERSION = SHA-256 of the exact bytes,
// which is what tools/codegen/geoindex.mjs puts into the backend index and what the
// operations summary returns as scope.geoIndexVersion. Equal versions prove that the
// browser's region tree and the server's region statistics use the same table.
import type { RegionManifestEntry } from './region-path';

export type GeoManifestEntry = RegionManifestEntry & {
  name: string;
  center: [number, number] | null;
  childrenFile: string | null;
  boundaryFile: string | null;
};
export type GeoManifest = Record<string, GeoManifestEntry>;

let cached: Promise<{ manifest: GeoManifest; version: string | null }> | undefined;

async function digest(bytes: ArrayBuffer): Promise<string | null> {
  // SubtleCrypto exists only in secure contexts (https, localhost); elsewhere the version is unknown.
  if (!globalThis.crypto?.subtle) return null;
  const hash = await crypto.subtle.digest('SHA-256', bytes);
  return [...new Uint8Array(hash)].map((b) => b.toString(16).padStart(2, '0')).join('');
}

function load() {
  if (!cached)
    cached = fetch(`${import.meta.env.BASE_URL}geo/geo-manifest.json`)
      .then(async (res) => {
        if (!res.ok) throw new Error('地图清单加载失败');
        const bytes = await res.arrayBuffer();
        return { manifest: JSON.parse(new TextDecoder().decode(bytes)) as GeoManifest, version: await digest(bytes) };
      })
      .catch((e) => {
        cached = undefined; // allow a retry
        throw e;
      });
  return cached;
}

export function loadGeoManifest(): Promise<GeoManifest> {
  return load().then((r) => r.manifest);
}

/** SHA-256 (hex) of the manifest bytes, or null when it cannot be computed in this context. */
export function loadGeoManifestVersion(): Promise<string | null> {
  return load().then((r) => r.version);
}

/** Entries whose parent is `code` (the next drill level), in manifest order. */
export function childEntries(manifest: GeoManifest, code: number): GeoManifestEntry[] {
  return Object.values(manifest).filter((r) => r.parent === code);
}
