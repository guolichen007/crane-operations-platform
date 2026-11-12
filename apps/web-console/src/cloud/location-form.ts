// Q4-QA-R1 §4: minimal location create form (pure logic, unit-tested). Mirrors the AssetLocation
// contract (contracts/model.json); the server validates again and is the authority.
import type { AssetLocation } from '../../../../contracts/typescript/types.gen';
import { ApiError } from './client';

export const LOCATION_KINDS = ['SITE', 'PLANT', 'WORKSHOP', 'BAY', 'RUNWAY', 'OTHER'] as const;
export const KIND_TEXT: Record<string, string> = {
  SITE: '场地', PLANT: '厂区', WORKSHOP: '车间', BAY: '工位', RUNWAY: '轨道', OTHER: '其他',
};

export type LocationForm = { kind: AssetLocation['kind'] | ''; name: string; code: string; parentId: string; address: string };
export const blankLocation = (): LocationForm => ({ kind: '', name: '', code: '', parentId: '', address: '' });
export const locationDirty = (f: LocationForm) => JSON.stringify(f) !== JSON.stringify(blankLocation());

export function validateLocation(f: LocationForm): Partial<Record<keyof LocationForm, string>> {
  const e: Partial<Record<keyof LocationForm, string>> = {};
  if (!LOCATION_KINDS.includes(f.kind as (typeof LOCATION_KINDS)[number])) e.kind = '请选择位置类型';
  if (!f.name.trim()) e.name = '请输入名称';
  else if ([...f.name.trim()].length > 256) e.name = '不超过 256 字';
  if ([...f.code.trim()].length > 160) e.code = '不超过 160 字';
  if ([...f.address].length > 240) e.address = '不超过 240 字';
  return e;
}

/**
 * The create body. Coordinates are never invented: longitude/latitude are null and the coordinate
 * system UNKNOWN (the user enters no coordinates here).
 */
export function toNewLocation(projectId: string, f: LocationForm): AssetLocation {
  return {
    id: '',
    projectId,
    parentId: f.parentId || null,
    kind: f.kind as AssetLocation['kind'],
    code: f.code.trim() || null,
    name: f.name.trim(),
    province: '',
    city: '',
    county: '',
    longitude: null,
    latitude: null,
    coordinateSystem: 'UNKNOWN',
    coordinateSource: 'MANUAL',
    status: 'ACTIVE',
    version: 1,
    address: f.address.trim(),
  };
}

export function locationErrorText(e: unknown): string {
  if (!(e instanceof ApiError)) return '网络连接失败或请求超时，请稍后刷新列表确认是否已创建。';
  switch (e.status) {
    case 400: return '请求格式错误，未创建。';
    case 422: return `输入不符合要求，未创建：${e.message}`;
    case 409: return '该编号在本项目中已存在，未创建。请更换编号或留空。';
    case 403: return '没有在该项目创建位置的权限（需要项目管理员）。';
    case 404: return '项目不存在或已不在您的授权范围内。';
    case 503: return '服务暂不可用，请稍后刷新列表确认是否已创建（输入已保留）。';
    default: return `创建失败（${e.status}）：${e.message}`;
  }
}
