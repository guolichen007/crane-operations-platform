// Q3: formal project → Inventory registration (pure logic, unit-tested).
//
// The user picks a province → prefecture → county from the SAME geo manifest the map uses
// (public/geo/geo-manifest.json); the selected adcode is sent, and the server derives the
// names from its index of the same manifest. No coordinates are entered or stored: the map
// places the project at ADMINISTRATIVE-REGION precision only.
import type { InventoryProject, ProjectInventoryRegistration } from '../../../../contracts/typescript/types.gen';
import type { GeoManifest, GeoManifestEntry } from '../scene/geo-manifest';
import { ApiError } from './client';

export const LIFECYCLES = [
  { value: 'PLANNED', label: '计划实施' },
  { value: 'PRE_DEVELOPMENT', label: '软件预开发' },
  { value: 'COMPLETED', label: '已完成' },
] as const;

export type RegistrationForm = {
  lifecycle: ProjectInventoryRegistration['lifecycle'] | '';
  province: string; // adcode as string ('' = none)
  prefecture: string;
  county: string;
  locationLabel: string;
  scheduleText: string;
  notes: string;
};

export const blankRegistration = (): RegistrationForm => ({
  lifecycle: '', province: '', prefecture: '', county: '', locationLabel: '', scheduleText: '', notes: '',
});

export const registrationDirty = (f: RegistrationForm) => JSON.stringify(f) !== JSON.stringify(blankRegistration());

const byName = (a: GeoManifestEntry, b: GeoManifestEntry) => a.adcode - b.adcode;
export function provinces(m: GeoManifest): GeoManifestEntry[] {
  return Object.values(m).filter((r) => r.level === 'province').sort(byName);
}
export function childrenOf(m: GeoManifest, code: string): GeoManifestEntry[] {
  if (!code) return [];
  return Object.values(m).filter((r) => r.parent === Number(code)).sort(byName);
}

/** The most specific selected code: county > prefecture > province; null when none. */
export function selectedAdminCode(f: RegistrationForm): number | null {
  const v = f.county || f.prefecture || f.province;
  return v ? Number(v) : null;
}

/** Human description of the precision actually achieved by the selection. */
export function precisionText(m: GeoManifest | undefined, f: RegistrationForm): string {
  const code = selectedAdminCode(f);
  if (!m || code === null) return '尚未选择行政区';
  const entry = m[String(code)];
  const level = entry?.level === 'county' ? '区县级' : entry?.level === 'prefecture' ? '地市级' : '省级';
  const names = [f.province, f.prefecture, f.county].filter(Boolean).map((c) => m[c]?.name).filter(Boolean).join(' ');
  return `${names}（${level}行政区定位，非设备精确坐标）`;
}

export function validateRegistration(f: RegistrationForm, m: GeoManifest | undefined): Partial<Record<keyof RegistrationForm, string>> {
  const e: Partial<Record<keyof RegistrationForm, string>> = {};
  if (!LIFECYCLES.some((l) => l.value === f.lifecycle)) e.lifecycle = '请选择项目生命周期';
  if (!m) e.province = '行政区数据未加载，暂不能登记';
  else if (!f.province) e.province = '请选择省份';
  else {
    // Consistency: every selected level must be a child of the previous one.
    if (f.prefecture && m[f.prefecture]?.parent !== Number(f.province)) e.prefecture = '地市与省份不匹配';
    if (f.county && m[f.county]?.parent !== Number(f.prefecture || f.province)) e.county = '区县与地市不匹配';
  }
  if ([...f.locationLabel].length > 240) e.locationLabel = '不超过 240 字';
  if ([...f.scheduleText].length > 400) e.scheduleText = '不超过 400 字';
  if ([...f.notes].length > 1000) e.notes = '不超过 1000 字';
  return e;
}

export function toRegistration(f: RegistrationForm): ProjectInventoryRegistration {
  return {
    lifecycle: f.lifecycle as ProjectInventoryRegistration['lifecycle'],
    adminCode: selectedAdminCode(f)!,
    locationLabel: f.locationLabel.trim(),
    scheduleText: f.scheduleText.trim(),
    notes: f.notes.trim(),
  };
}

export function registrationErrorMessage(e: unknown): string {
  if (e instanceof ApiError) {
    switch (e.status) {
      case 401: return '登录已失效，请重新登录。';
      case 403: return '无权登记：需要该项目的「项目管理员」权限（位置与资产管理）。';
      case 404: return '项目不存在、尚未批准，或您没有该项目的授权。';
      case 409: return '该项目已登记 Inventory（可能已被他人登记），列表已刷新。';
      case 422: return `输入不符合要求：${e.message}`;
      case 503: return e.message.includes('administrative') ? '服务端行政区数据暂不可用，未做任何修改，请稍后重试。' : '服务暂不可用，未确认是否登记成功，列表已刷新。';
      default: return `请求失败（${e.status}）：${e.message}`;
    }
  }
  return '网络连接失败或请求超时；请刷新后确认是否已登记。已输入的内容已保留。';
}

/** Runtime source of an Inventory project, from facts only (Q3 principle 7/8). */
export function runtimeSourceText(p: Pick<InventoryProject, 'runtimeDeviceCount' | 'realConnectedCount' | 'simulatedCount'>): string {
  if (p.runtimeDeviceCount === 0) return '尚未接入运行源';
  if (p.realConnectedCount > 0) return `真实在线 ${p.realConnectedCount}`;
  if (p.simulatedCount > 0) return `仅模拟运行源 ${p.simulatedCount}（非真实在线）`;
  return '已登记运行身份 · 当前无真实在线';
}
