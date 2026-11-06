import type { Equipment, Input } from './model';

// Field-level three-way merge for project drafts.
//   base   = the server version this editor last loaded or saved
//   local  = what the user has in the form now
//   remote = the server version that caused the 409
// A unit changed on one side only takes that side. A unit changed on both
// sides to different values is a conflict and needs an explicit choice.
// Numeric values are merged together with their knowledge flag so that
// null/0/LEGACY_UNCONFIRMED states never split.

export type Side = 'local' | 'remote';
export interface MergeUnit {
  key: string;
  label: string;
  local: string;
  remote: string;
}
export interface MergeResult {
  merged: Input;
  conflicts: MergeUnit[];
  /** Units taken from the server because only the other editor changed them. */
  fromRemote: string[];
  /** Units kept from this editor because only this editor changed them. */
  fromLocal: string[];
  /** Human-readable "label：value" for both lists, in field order. */
  remoteChanges: string[];
  localChanges: string[];
}

const minute = (v: unknown) => (typeof v === 'string' && v ? Math.floor(new Date(v).getTime() / 60000) : null);
function norm(v: unknown): unknown {
  if (v === undefined || v === null || v === '') return null;
  if (Array.isArray(v)) return v.length ? v.map(norm) : null;
  if (typeof v === 'object') {
    const out: Record<string, unknown> = {};
    for (const k of Object.keys(v as object).sort()) {
      const n = norm((v as Record<string, unknown>)[k]);
      if (n !== null) out[k] = n;
    }
    return Object.keys(out).length ? out : null;
  }
  return v;
}
const same = (a: unknown, b: unknown) => JSON.stringify(norm(a)) === JSON.stringify(norm(b));
const sortedSet = (v: unknown) => (Array.isArray(v) ? [...v].sort() : v);

const numericKeys = ['quantity', 'capacityTons', 'spanM', 'liftHeightM'] as const;
const deviceKeys = ['equipmentType', 'manufacturer', 'model', 'controlMode', 'plcBrand', 'plcModel', 'driveBrand', 'notes', 'confidenceLevel'] as const;
const deviceSetKeys = ['environmentFlags', 'automationGoals'] as const;
const locationKeys = ['province', 'city', 'district', 'industrialPark', 'factoryName', 'workshop', 'addressDetail'] as const;
const scalarKeys = ['projectName', 'customer', 'salesStage', 'projectType', 'informationSource', 'confidenceLevel', 'expectedPurchaseDate', 'requirementNotes', 'tenderNo', 'notes', 'nextAction'] as const;

export const fieldLabels: Record<string, string> = {
  projectName: '项目名称', customer: '终端客户', salesStage: '销售阶段', projectType: '项目类型', informationSource: '项目来源',
  confidenceLevel: '信息可信度', expectedPurchaseDate: '预期采购日期', requirementNotes: '主要需求说明', tenderNo: '招标编号', notes: '补充说明',
  nextAction: '下一步事项', nextFollowupAt: '下次跟进时间', estimatedAmount: '预估金额', requirements: '自动化需求分类', parties: '关系方',
  plannedEquipment: '计划设备（增删或顺序变化）',
  'location.province': '省', 'location.city': '市', 'location.district': '区县', 'location.industrialPark': '工业园区',
  'location.factoryName': '厂区', 'location.workshop': '车间', 'location.addressDetail': '详细地址',
  quantity: '数量', capacityTons: '额定起重量', spanM: '跨度', liftHeightM: '起升高度', equipmentType: '设备类型', manufacturer: '厂家',
  model: '型号', controlMode: '控制方式', plcBrand: 'PLC 品牌', plcModel: 'PLC 型号', driveBrand: '驱动品牌', environmentFlags: '工作环境',
  automationGoals: '设备自动化目标',
};
const unitLabel = (key: string) => {
  const m = /^plannedEquipment\.(\d+)\.(.+)$/.exec(key);
  if (m) return `计划设备 ${Number(m[1]) + 1} · ${fieldLabels[m[2]] || m[2]}`;
  return fieldLabels[key] || key;
};

interface Unit {
  key: string;
  get: (x: Input) => unknown;
  set: (target: Input, from: Input) => void;
  show: (x: Input) => string;
}
const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v ?? null));
const text = (v: unknown) => {
  if (v === null || v === undefined || v === '' || (Array.isArray(v) && !v.length)) return '空';
  if (Array.isArray(v)) return v.map((x) => (typeof x === 'object' ? JSON.stringify(x) : String(x))).join('、');
  return String(v);
};
const numText = (v: unknown, state: unknown) =>
  v === null || v === undefined ? '未知' : state === 'LEGACY_UNCONFIRMED' && v === 0 ? '0（历史待确认）' : String(v);

function topUnits(): Unit[] {
  const units: Unit[] = scalarKeys.map((k) => ({
    key: k,
    get: (x) => x[k],
    set: (t, f) => ((t as unknown as Record<string, unknown>)[k] = clone(f[k])),
    show: (x) => text(x[k]),
  }));
  for (const k of locationKeys)
    units.push({ key: 'location.' + k, get: (x) => x.location?.[k], set: (t, f) => (t.location[k] = f.location?.[k] ?? ''), show: (x) => text(x.location?.[k]) });
  units.push({
    key: 'nextFollowupAt',
    get: (x) => minute(x.nextFollowupAt),
    set: (t, f) => (t.nextFollowupAt = f.nextFollowupAt ?? null),
    show: (x) => (x.nextFollowupAt ? new Date(x.nextFollowupAt).toLocaleString('zh-CN') : '空'),
  });
  units.push({
    key: 'estimatedAmount',
    get: (x) => [x.estimatedAmount ?? null, x.knowledge?.estimatedAmount ?? null],
    set: (t, f) => {
      t.estimatedAmount = f.estimatedAmount ?? null;
      t.knowledge = { ...(t.knowledge || {}) };
      if (f.knowledge?.estimatedAmount) t.knowledge.estimatedAmount = f.knowledge.estimatedAmount;
      else delete t.knowledge.estimatedAmount;
    },
    show: (x) => numText(x.estimatedAmount, x.knowledge?.estimatedAmount),
  });
  units.push({ key: 'requirements', get: (x) => sortedSet(x.requirements), set: (t, f) => (t.requirements = clone(f.requirements) || []), show: (x) => text(x.requirements) });
  units.push({ key: 'parties', get: (x) => x.parties, set: (t, f) => (t.parties = clone(f.parties) || []), show: (x) => text((x.parties || []).map((p) => p.name)) });
  return units;
}
function deviceUnits(i: number): Unit[] {
  const dev = (x: Input): Partial<Equipment> => x.plannedEquipment?.[i] || {};
  const tgt = (t: Input) => t.plannedEquipment[i] as unknown as Record<string, unknown> & { knowledge?: Record<string, string> };
  const units: Unit[] = [];
  for (const k of numericKeys)
    units.push({
      key: `plannedEquipment.${i}.${k}`,
      get: (x) => [dev(x)[k] ?? null, dev(x).knowledge?.[k] ?? null],
      set: (t, f) => {
        const d = tgt(t);
        d[k] = dev(f)[k] ?? null;
        d.knowledge = { ...(d.knowledge || {}) };
        const s = dev(f).knowledge?.[k];
        if (s) d.knowledge[k] = s;
        else delete d.knowledge[k];
      },
      show: (x) => numText(dev(x)[k], dev(x).knowledge?.[k]),
    });
  for (const k of deviceKeys)
    units.push({ key: `plannedEquipment.${i}.${k}`, get: (x) => dev(x)[k], set: (t, f) => (tgt(t)[k] = clone(dev(f)[k]) ?? ''), show: (x) => text(dev(x)[k]) });
  for (const k of deviceSetKeys)
    units.push({ key: `plannedEquipment.${i}.${k}`, get: (x) => sortedSet(dev(x)[k]), set: (t, f) => (tgt(t)[k] = clone(dev(f)[k]) || []), show: (x) => text(dev(x)[k]) });
  return units;
}

/**
 * Compute a three-way merge. `choices` resolves conflicts by unit key.
 * Devices have no stable identity; they are merged field by field only when
 * all three versions have the same count, the same type in every slot, and no
 * type repeats. Otherwise, if both sides changed the list, it is one conflict
 * unit chosen by the user.
 */
export function merge(base: Input, local: Input, remote: Input, choices: Record<string, Side> = {}): MergeResult {
  const merged = clone(remote) as Input;
  merged.location = { ...remote.location };
  const conflicts: MergeUnit[] = [];
  const fromRemote: string[] = [];
  const fromLocal: string[] = [];
  const remoteChanges: string[] = [];
  const localChanges: string[] = [];
  const apply = (u: Unit) => {
    const l = u.get(local), r = u.get(remote), b = u.get(base);
    if (same(l, r)) return;
    if (same(l, b)) {
      fromRemote.push(u.key);
      remoteChanges.push(unitLabel(u.key) + '：' + u.show(remote));
      return; // merged already holds remote
    }
    if (same(r, b)) {
      u.set(merged, local);
      fromLocal.push(u.key);
      localChanges.push(unitLabel(u.key) + '：' + u.show(local));
      return;
    }
    const choice = choices[u.key];
    if (choice === 'local') u.set(merged, local);
    conflicts.push({ key: u.key, label: unitLabel(u.key), local: u.show(local), remote: u.show(remote) });
  };
  topUnits().forEach(apply);

  const lb = base.plannedEquipment?.length ?? 0, ll = local.plannedEquipment?.length ?? 0, lr = remote.plannedEquipment?.length ?? 0;
  const listUnit: Unit = {
    key: 'plannedEquipment',
    get: (x) => x.plannedEquipment,
    set: (t, f) => (t.plannedEquipment = clone(f.plannedEquipment) || []),
    show: (x) =>
      (x.plannedEquipment || []).length
        ? (x.plannedEquipment || []).map((e, i) => `${i + 1}. ${e.equipmentType} × ${e.quantity ?? '未知'}`).join('；')
        : '无',
  };
  // Devices have no stable identity. Field-by-field merging by position is only
  // safe when both sides kept the same devices in the same slots (same count
  // and every equipmentType unchanged). Any reorder, insert, delete or type
  // change on a side makes positions untrustworthy: if the other side also
  // touched the devices, the whole list is one manual decision.
  // Position is used as identity only when it is unambiguous: the type at each
  // slot is unchanged on both sides AND no type occurs twice. Two units of the
  // same type can be swapped or retyped invisibly (model/maker are editable
  // fields, not identity), so with duplicates we never guess.
  const types = (x: Input) => (x.plannedEquipment || []).map((e) => e.equipmentType);
  const typesOf = (x: Input) => JSON.stringify(types(x));
  const unique = new Set(types(base)).size === lb;
  const aligned = unique && lb === ll && ll === lr && typesOf(base) === typesOf(local) && typesOf(base) === typesOf(remote);
  if (aligned) {
    for (let i = 0; i < lb; i++) deviceUnits(i).forEach(apply);
  } else {
    apply(listUnit);
  }
  return { merged, conflicts, fromRemote, fromLocal, remoteChanges, localChanges };
}

export const unresolved = (r: MergeResult, choices: Record<string, Side>) => r.conflicts.filter((c) => !choices[c.key]);
export { unitLabel };
