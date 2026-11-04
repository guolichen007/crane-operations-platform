import { api, pageQuery } from './client';
import type {
  AssetMasterData,
  AssetLocation,
  Manufacturer,
  ManufacturerInput,
  EquipmentCatalogItem,
  EquipmentInstallation,
  EquipmentRelation,
} from '../../../../contracts/typescript/types.gen';

export type AssetListParams = {
  limit?: number;
  offset?: number;
  sort?: string;
  q?: string;
  status?: string;
  type?: string;
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
    },
  });
export const getAsset = (id: string) => api<AssetMasterData>(`assets/${encodeURIComponent(id)}`);
export const createAsset = (input: AssetMasterData, idempotencyKey: string) =>
  api<AssetMasterData>('assets', { method: 'POST', body: input, idempotencyKey });
export const updateAsset = (id: string, input: AssetMasterData, version: number) =>
  api<AssetMasterData>(`assets/${encodeURIComponent(id)}`, { method: 'PATCH', body: input, ifMatch: version });
export const retireAsset = (id: string, version: number) =>
  api<{ status: string }>(`assets/${encodeURIComponent(id)}/retire`, { method: 'POST', body: {}, ifMatch: version });
export const moveAsset = (id: string, locationId: string, version: number) =>
  api<{ status: string }>(`assets/${encodeURIComponent(id)}/move`, { method: 'POST', body: { locationId }, ifMatch: version });

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
