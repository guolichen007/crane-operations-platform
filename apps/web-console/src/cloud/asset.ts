import { api, pageQuery } from './client';
import { EQUIPMENT_TYPE_TEXT } from './zh';
import type {
  AssetMasterData,
  AssetLocation,
  Manufacturer,
  ManufacturerInput,
  EquipmentCatalogItem,
  EquipmentInstallation,
  EquipmentRelation,
  EquipmentType,
  AssetLocationReadback,
  AssetInstallationHistory,
  AssetLocationPathNode,
} from '../../../../contracts/typescript/types.gen';

export type AssetListParams = {
  limit?: number;
  offset?: number;
  sort?: string;
  q?: string;
  status?: string;
  type?: string;
  project?: string;
};
export type LocationListParams = {
  limit?: number;
  offset?: number;
  sort?: string;
  q?: string;
  kind?: string;
  status?: string;
  parentId?: string;
};

// ---- Asset master ----
export const listAssets = (params: AssetListParams = {}) =>
  api<AssetMasterData[]>('assets', {
    query: {
      ...pageQuery(params.limit ?? 50, params.offset ?? 0, params.sort, params.q),
      status: params.status,
      type: params.type,
      project: params.project,
    },
  });
export const getAsset = (id: string) => api<AssetMasterData>(`assets/${encodeURIComponent(id)}`);
export const createAsset = (input: AssetMasterData, idempotencyKey: string) =>
  api<AssetMasterData>('assets', { method: 'POST', body: input, idempotencyKey });

// Business fields a PATCH may carry (identity, project and version are never sent;
// the version travels only in If-Match).
export type AssetPatch = Partial<Omit<AssetMasterData, 'id' | 'projectId' | 'version'>>;

/** Only the business fields whose value differs from the base snapshot. */
export function assetChanges(base: AssetMasterData, draft: AssetMasterData): AssetPatch {
  const out: Record<string, unknown> = {};
  for (const key of Object.keys(draft) as (keyof AssetMasterData)[]) {
    if (key === 'id' || key === 'projectId' || key === 'version') continue;
    if (JSON.stringify(draft[key] ?? null) !== JSON.stringify(base[key] ?? null)) out[key] = draft[key] ?? null;
  }
  return out as AssetPatch;
}

export const updateAsset = (id: string, patch: AssetPatch, version: number) =>
  api<AssetMasterData>(`assets/${encodeURIComponent(id)}`, { method: 'PATCH', body: patch, ifMatch: version });
export const retireAsset = (id: string, version: number) =>
  api<{ status: string }>(`assets/${encodeURIComponent(id)}/retire`, { method: 'POST', body: {}, ifMatch: version });
export const moveAsset = (id: string, locationId: string, version: number) =>
  api<{ status: string }>(`assets/${encodeURIComponent(id)}/move`, { method: 'POST', body: { locationId }, ifMatch: version });

// ---- R6-BIZ-R1 F-03: current installation location (read-only) ----
/** Current location (current=null when the asset was never installed) — never a coordinate. */
export const getAssetLocation = (id: string) => api<AssetLocationReadback>(`assets/${encodeURIComponent(id)}/location`);
export const listAssetInstallationHistory = (id: string, limit = 20, offset = 0) =>
  api<AssetInstallationHistory>(`assets/${encodeURIComponent(id)}/installation-history`, { query: { limit, offset } });
/** 「场地 › 厂区 › 车间」 from the root→leaf path; each level shows its name (and code when present). */
export function locationPathText(path: readonly AssetLocationPathNode[], kindText: Record<string, string>): string {
  return path.map((n) => `${kindText[n.kind] ?? n.kind}·${n.name}${n.code ? `（${n.code}）` : ''}`).join(' › ');
}

// ---- Location ----
export const listLocations = (projectId: string, params: LocationListParams = {}) =>
  api<AssetLocation[]>(`projects/${encodeURIComponent(projectId)}/locations`, {
    query: {
      ...pageQuery(params.limit ?? 100, params.offset ?? 0, params.sort, params.q),
      kind: params.kind,
      status: params.status,
      parentId: params.parentId,
    },
  });
// listAllLocations pages through the server's 100-item window so a project with
// more than 100 nodes is fully browsable instead of silently truncated. Used by
// both the location tree and the asset move-target picker.
export async function listAllLocations(projectId: string, params: LocationListParams = {}): Promise<AssetLocation[]> {
  const all: AssetLocation[] = [];
  const pageSize = 100;
  for (let offset = 0; ; offset += pageSize) {
    const page = await listLocations(projectId, { ...params, limit: pageSize, offset });
    all.push(...page);
    if (page.length < pageSize) break;
  }
  return all;
}
export const getLocation = (id: string) => api<AssetLocation>(`locations/${encodeURIComponent(id)}`);
export const createLocation = (input: AssetLocation) =>
  api<AssetLocation>(`projects/${encodeURIComponent(input.projectId)}/locations`, { method: 'POST', body: input });
export const updateLocation = (id: string, input: AssetLocation, version: number) =>
  api<AssetLocation>(`locations/${encodeURIComponent(id)}`, { method: 'PATCH', body: input, ifMatch: version });
export const disableLocation = (id: string, version: number) =>
  api<{ status: string }>(`locations/${encodeURIComponent(id)}/disable`, { method: 'POST', body: {}, ifMatch: version });

// ---- Manufacturer ----
export const listManufacturers = (params: AssetListParams = {}) =>
  api<Manufacturer[]>('manufacturers', { query: pageQuery(params.limit ?? 100, params.offset ?? 0, params.sort, params.q) });
export const getManufacturer = (id: string) => api<Manufacturer>(`manufacturers/${encodeURIComponent(id)}`);
export const createManufacturer = (input: ManufacturerInput) =>
  api<Manufacturer>('manufacturers', { method: 'POST', body: input });
export const updateManufacturer = (id: string, input: Manufacturer, version: number) =>
  api<Manufacturer>(`manufacturers/${encodeURIComponent(id)}`, { method: 'PATCH', body: input, ifMatch: version });
export const disableManufacturer = (id: string, version: number) =>
  api<{ status: string }>(`manufacturers/${encodeURIComponent(id)}/disable`, { method: 'POST', body: {}, ifMatch: version });

// ---- Catalog ----
export const listCatalog = (params: AssetListParams = {}) =>
  api<EquipmentCatalogItem[]>('equipment/catalog', { query: pageQuery(params.limit ?? 100, params.offset ?? 0, params.sort, params.q) });
export const getCatalogItem = (id: string) => api<EquipmentCatalogItem>(`equipment/catalog/${encodeURIComponent(id)}`);
export const createCatalogItem = (input: EquipmentCatalogItem) =>
  api<EquipmentCatalogItem>('equipment/catalog', { method: 'POST', body: input });
export const updateCatalogItem = (id: string, input: EquipmentCatalogItem, version: number) =>
  api<EquipmentCatalogItem>(`equipment/catalog/${encodeURIComponent(id)}`, { method: 'PATCH', body: input, ifMatch: version });
export const disableCatalogItem = (id: string, version: number) =>
  api<{ status: string }>(`equipment/catalog/${encodeURIComponent(id)}/disable`, { method: 'POST', body: {}, ifMatch: version });

// ---- Equipment ----
/** Code-owned equipment type dictionary (stable code + Chinese name). */
export const listEquipmentTypes = () => api<EquipmentType[]>('equipment/types');
/** "中文名（CODE）" from the dictionary (live list first, then the 51-type code dictionary); an unknown
 * code is never renamed into a guessed name — it reads 「未登记的设备类型（编码 X）」 (R9-UX). */
export function equipmentTypeLabel(code: string, types: readonly EquipmentType[] | undefined): string {
  const t = types?.find((x) => x.typeCode === code);
  if (t) return `${t.displayName}（${t.typeCode}）`;
  if (!code) return '—';
  return EQUIPMENT_TYPE_TEXT[code] ? `${EQUIPMENT_TYPE_TEXT[code]}（${code}）` : `未登记的设备类型（编码 ${code}）`;
}
export const listEquipment = (assetId: string, params: AssetListParams = {}) =>
  api<EquipmentInstallation[]>(`assets/${encodeURIComponent(assetId)}/equipment`, {
    query: pageQuery(params.limit ?? 100, params.offset ?? 0, params.sort, params.q),
  });
export const getEquipment = (id: string) => api<EquipmentInstallation>(`equipment/${encodeURIComponent(id)}`);
export const createEquipment = (input: EquipmentInstallation & { spec?: unknown }, idempotencyKey: string) =>
  api<EquipmentInstallation>(`assets/${encodeURIComponent(input.assetId)}/equipment`, { method: 'POST', body: input, idempotencyKey });
export const updateEquipment = (id: string, input: EquipmentInstallation, version: number) =>
  api<EquipmentInstallation>(`equipment/${encodeURIComponent(id)}`, { method: 'PATCH', body: input, ifMatch: version });
export const removeEquipment = (id: string, version: number) =>
  api<{ status: string }>(`equipment/${encodeURIComponent(id)}/remove`, { method: 'POST', body: {}, ifMatch: version });

// ---- Relations ----
export const listRelations = (assetId: string, params: AssetListParams = {}) =>
  api<EquipmentRelation[]>(`assets/${encodeURIComponent(assetId)}/equipment-relations`, {
    query: pageQuery(params.limit ?? 100, params.offset ?? 0),
  });
export const createRelation = (input: EquipmentRelation) =>
  api<EquipmentRelation>(`assets/${encodeURIComponent(input.assetId)}/equipment-relations`, { method: 'POST', body: input });
export const removeRelation = (id: string, version: number) =>
  api<{ status: string }>(`equipment-relations/${encodeURIComponent(id)}/remove`, { method: 'POST', body: {}, ifMatch: version });
