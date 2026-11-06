import { describe, it, expect } from 'vitest';
import { merge, unresolved } from './merge';
import { blank, blankEquipment, type Input } from './model';

const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v));
function baseInput(): Input {
  const b = blank();
  b.projectName = '原名';
  b.customer = '原客户';
  b.location.province = '合成省';
  b.location.city = '合成市';
  b.requirementNotes = '原需求';
  b.requirements = ['ANTI_SWAY', 'POSITIONING'];
  b.estimatedAmount = 100;
  b.knowledge = { estimatedAmount: 'KNOWN' };
  b.nextFollowupAt = '2027-01-01T02:00:00.000Z';
  b.parties = [{ role: 'EPC', name: '原 EPC' }];
  const e = blankEquipment();
  e.quantity = 2;
  e.capacityTons = 20;
  e.spanM = 18;
  e.knowledge = { quantity: 'KNOWN', capacityTons: 'KNOWN', spanM: 'KNOWN' };
  e.environmentFlags = ['DUST'];
  b.plannedEquipment = [e];
  return b;
}

describe('三向合并', () => {
  it('互不相交的修改全部保留', () => {
    const base = baseInput(), local = clone(base), remote = clone(base);
    local.projectName = 'A 改名';
    local.plannedEquipment[0].spanM = 22.5;
    remote.requirementNotes = 'B 补充';
    remote.plannedEquipment[0].capacityTons = 32;
    remote.estimatedAmount = null;
    remote.knowledge = { estimatedAmount: 'UNKNOWN' };
    remote.parties = [{ role: 'EPC', name: '新 EPC' }];
    const r = merge(base, local, remote);
    expect(r.conflicts).toEqual([]);
    expect(r.merged.projectName).toBe('A 改名');
    expect(r.merged.requirementNotes).toBe('B 补充');
    expect(r.merged.plannedEquipment[0]).toMatchObject({ spanM: 22.5, capacityTons: 32, quantity: 2 });
    expect(r.merged.estimatedAmount).toBeNull();
    expect(r.merged.knowledge?.estimatedAmount).toBe('UNKNOWN');
    expect(r.merged.parties[0].name).toBe('新 EPC');
  });
  it('同一字段双方改成不同值必须选择，未选时不写入本地值', () => {
    const base = baseInput(), local = clone(base), remote = clone(base);
    local.projectName = 'A';
    remote.projectName = 'B';
    const r = merge(base, local, remote);
    expect(r.conflicts.map((c) => c.key)).toEqual(['projectName']);
    expect(unresolved(r, {})).toHaveLength(1);
    expect(r.merged.projectName).toBe('B');
    expect(merge(base, local, remote, { projectName: 'local' }).merged.projectName).toBe('A');
    expect(merge(base, local, remote, { projectName: 'remote' }).merged.projectName).toBe('B');
  });
  it('双方改成相同值不算冲突', () => {
    const base = baseInput(), local = clone(base), remote = clone(base);
    local.customer = remote.customer = '同改';
    expect(merge(base, local, remote).conflicts).toEqual([]);
  });
  it('数值与已知性一起合并：未知、真实 0、历史待确认 0 互不混淆', () => {
    const base = baseInput();
    base.estimatedAmount = 0;
    base.knowledge = { estimatedAmount: 'LEGACY_UNCONFIRMED' };
    const local = clone(base), remote = clone(base);
    local.knowledge = { estimatedAmount: 'KNOWN' }; // 本地确认为真实 0
    const r = merge(base, local, remote);
    expect(r.merged.estimatedAmount).toBe(0);
    expect(r.merged.knowledge?.estimatedAmount).toBe('KNOWN');
    remote.estimatedAmount = null;
    remote.knowledge = { estimatedAmount: 'UNKNOWN' };
    const c = merge(base, local, remote);
    expect(c.conflicts.map((x) => x.key)).toEqual(['estimatedAmount']);
    expect(c.conflicts[0].local).toBe('0');
    expect(c.conflicts[0].remote).toBe('未知');
  });
  it('设备数量相同按字段合并；一方增删设备另一方未改列表则采纳该方', () => {
    const base = baseInput(), local = clone(base), remote = clone(base);
    local.plannedEquipment[0].environmentFlags = ['DUST', 'OUTDOOR'];
    remote.plannedEquipment[0].controlMode = 'REMOTE';
    let r = merge(base, local, remote);
    expect(r.conflicts).toEqual([]);
    expect(r.merged.plannedEquipment[0]).toMatchObject({ controlMode: 'REMOTE', environmentFlags: ['DUST', 'OUTDOOR'] });
    const added = clone(base);
    added.plannedEquipment.push(blankEquipment());
    r = merge(base, clone(base), added);
    expect(r.merged.plannedEquipment).toHaveLength(2);
    const removed = clone(base);
    removed.plannedEquipment = [];
    r = merge(base, removed, clone(base));
    expect(r.merged.plannedEquipment).toHaveLength(0);
  });
  it('双方都改设备列表且数量不一致时整体作为一个冲突，不按下标猜测', () => {
    const base = baseInput(), local = clone(base), remote = clone(base);
    local.plannedEquipment = [];
    remote.plannedEquipment[0].spanM = 30;
    remote.plannedEquipment.push(blankEquipment());
    const r = merge(base, local, remote);
    expect(r.conflicts.map((c) => c.key)).toEqual(['plannedEquipment']);
    expect(r.merged.plannedEquipment).toHaveLength(2);
    expect(merge(base, local, remote, { plannedEquipment: 'local' }).merged.plannedEquipment).toHaveLength(0);
  });
  it('跟进时间按分钟比较，集合字段不受顺序影响', () => {
    const base = baseInput(), local = clone(base), remote = clone(base);
    local.nextFollowupAt = '2027-01-01T02:00:30.000Z';
    local.requirements = ['POSITIONING', 'ANTI_SWAY'];
    expect(merge(base, local, remote).conflicts).toEqual([]);
    expect(merge(base, local, remote).fromLocal).toEqual([]);
  });
  it('每个可写字段都被合并覆盖：只在本地改动的值一定进入结果', () => {
    const base = baseInput();
    const edits: [string, (x: Input) => void, (x: Input) => unknown][] = [
      ['projectName', (x) => (x.projectName = 'v'), (x) => x.projectName],
      ['customer', (x) => (x.customer = 'v'), (x) => x.customer],
      ['salesStage', (x) => (x.salesStage = 'L2_TECHNICAL'), (x) => x.salesStage],
      ['projectType', (x) => (x.projectType = 'NEW_BUILD'), (x) => x.projectType],
      ['informationSource', (x) => (x.informationSource = 'EPC'), (x) => x.informationSource],
      ['confidenceLevel', (x) => (x.confidenceLevel = 'VERBAL'), (x) => x.confidenceLevel],
      ['expectedPurchaseDate', (x) => (x.expectedPurchaseDate = '2027-05-01'), (x) => x.expectedPurchaseDate],
      ['requirementNotes', (x) => (x.requirementNotes = 'v'), (x) => x.requirementNotes],
      ['tenderNo', (x) => (x.tenderNo = 'v'), (x) => x.tenderNo],
      ['notes', (x) => (x.notes = 'v'), (x) => x.notes],
      ['nextAction', (x) => (x.nextAction = 'v'), (x) => x.nextAction],
      ['nextFollowupAt', (x) => (x.nextFollowupAt = '2027-02-01T00:00:00.000Z'), (x) => x.nextFollowupAt],
      ['estimatedAmount', (x) => (x.estimatedAmount = 5), (x) => x.estimatedAmount],
      ['requirements', (x) => (x.requirements = ['SAFETY']), (x) => x.requirements],
      ['parties', (x) => (x.parties = []), (x) => x.parties],
      ...(['province', 'city', 'district', 'industrialPark', 'factoryName', 'workshop', 'addressDetail'] as const).map(
        (k) => ['location.' + k, (x: Input) => (x.location[k] = 'v'), (x: Input) => x.location[k]] as [string, (x: Input) => void, (x: Input) => unknown]
      ),
      ...(['quantity', 'capacityTons', 'spanM', 'liftHeightM'] as const).map(
        (k) => ['pe.' + k, (x: Input) => (x.plannedEquipment[0][k] = 7), (x: Input) => x.plannedEquipment[0][k]] as [string, (x: Input) => void, (x: Input) => unknown]
      ),
      ...(['equipmentType', 'manufacturer', 'model', 'controlMode', 'plcBrand', 'plcModel', 'driveBrand', 'notes', 'confidenceLevel'] as const).map(
        (k) => ['pe.' + k, (x: Input) => (x.plannedEquipment[0][k] = 'v'), (x: Input) => x.plannedEquipment[0][k]] as [string, (x: Input) => void, (x: Input) => unknown]
      ),
      ['pe.environmentFlags', (x) => (x.plannedEquipment[0].environmentFlags = ['OUTDOOR']), (x) => x.plannedEquipment[0].environmentFlags],
      ['pe.automationGoals', (x) => (x.plannedEquipment[0].automationGoals = ['VISION']), (x) => x.plannedEquipment[0].automationGoals],
    ];
    for (const [name, edit, read] of edits) {
      const local = clone(base), remote = clone(base);
      remote.notes = name === 'notes' ? remote.notes : '远端另改';
      edit(local);
      const r = merge(base, local, remote);
      expect(r.conflicts, name).toEqual([]);
      expect(read(r.merged), name).toEqual(read(local));
    }
    // Guard: the edit list must cover every top-level writable Input field.
    const covered = new Set(edits.map(([n]) => n.split('.')[0]));
    for (const k of Object.keys(blank()).filter((k) => !['version', 'knowledge'].includes(k)))
      expect(covered.has(k === 'location' ? 'location' : k === 'plannedEquipment' ? 'pe' : k), k).toBe(true);
  });
});
