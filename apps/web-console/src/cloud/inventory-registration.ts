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

/**
 * Message for a failed registration request, stating only what the response proves.
 * `provenUnwritten` = the server answered before any database work (validation / missing
 * administrative index); everything else must be verified against the Inventory read.
 */
export function registrationErrorMessage(e: unknown): { text: string; provenUnwritten: boolean } {
  if (e instanceof ApiError) {
    switch (e.status) {
      case 400: return { text: '请求格式错误，未被受理。', provenUnwritten: true };
      case 422: return { text: `输入不符合要求，未被受理：${e.message}`, provenUnwritten: true };
      case 401: return { text: '登录已失效。', provenUnwritten: false };
      case 403: return { text: '服务端拒绝：需要该项目的「项目管理员」权限，您的授权可能刚被调整。', provenUnwritten: false };
      case 404: return { text: '项目不存在、尚未批准，或您已没有该项目的授权。', provenUnwritten: false };
      case 409: return { text: '该项目已存在 Inventory 登记（可能由其他操作完成）。', provenUnwritten: false };
      case 503:
        if (e.message.includes('administrative')) return { text: '服务端行政区数据暂不可用，请求未被受理，可稍后重试。', provenUnwritten: true };
        return { text: '服务暂不可用。', provenUnwritten: false };
      default: return { text: `请求失败（${e.status}）：${e.message}`, provenUnwritten: false };
    }
  }
  return { text: '网络连接失败或请求超时，结果未知。', provenUnwritten: false };
}

export type VerifyOutcome =
  | { kind: 'registered'; text: string; project: InventoryProject; applied: 'same' | 'different' }
  | { kind: 'unverified'; text: string };

/**
 * Decide the outcome of an uncertain registration from POSITIVE server evidence only: the
 * caller's authorized Inventory read (GET /api/v1/projects) must contain the project with a
 * lifecycle. Absence (revoked grant, disabled account, scope change) or a failed read is
 * "unverified" — never success, never "nothing changed".
 */
export function verifyOutcome(id: string, submitted: ProjectInventoryRegistration, inventory: InventoryProject[] | null): VerifyOutcome {
  if (inventory === null) return { kind: 'unverified', text: '结果待核实：无法读取 Inventory，请稍后刷新页面查看。' };
  const p = inventory.find((x) => x.id === id);
  if (!p || !p.lifecycle) return { kind: 'unverified', text: '结果待核实：在您当前可读取的项目中未找到该项目的 Inventory 登记（授权可能已变化），不能确认是否已登记。' };
  const where = [p.province, p.city, p.county].filter(Boolean).join(' ') || `行政区 ${p.adminCode ?? '—'}`;
  const same = p.adminCode === submitted.adminCode && p.lifecycle === submitted.lifecycle;
  return same
    ? { kind: 'registered', applied: 'same', project: p, text: `${id} 已登记（服务端确认）：${where}。` }
    : { kind: 'registered', applied: 'different', project: p, text: `${id} 已由其他操作登记。服务端当前保存的是：${where}（${p.lifecycle}），本次表单内容未被应用。` };
}

/** Runtime source of an Inventory project, from facts only (Q3 principle 7/8). */
export function runtimeSourceText(p: Pick<InventoryProject, 'runtimeDeviceCount' | 'realConnectedCount' | 'simulatedCount'>): string {
  if (p.runtimeDeviceCount === 0) return '尚未接入运行源';
  if (p.realConnectedCount > 0) return `真实在线 ${p.realConnectedCount}`;
  if (p.simulatedCount > 0) return `仅模拟运行源 ${p.simulatedCount}（非真实在线）`;
  return '已登记运行身份 · 当前无真实在线';
}
