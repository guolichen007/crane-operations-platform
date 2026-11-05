import { describe, it, expect } from 'vitest';
import { useDraftSession, allowDiscard, allowClose, allowRebase, draftText, fieldDiff, sameData } from '../src/cloud/draft';

type A = { name: string; code: string | null; version: number };

describe('useDraftSession（固定版本 + 脏检查）', () => {
  it('打开时固定 baseVersion；之后外部数据变化不改变它', () => {
    const s = useDraftSession<A>();
    const server = { name: 'x', code: 'C1', version: 3 };
    s.start(server, server.version);
    server.version = 4; // simulated background refetch mutating the cached object
    server.name = 'changed-by-other-user';
    expect(s.baseVersion.value).toBe(3);
    expect(s.base.value?.name).toBe('x'); // deep-cloned, not aliased
  });
  it('未改动不脏；改动后脏；改回原值又不脏', () => {
    const s = useDraftSession<A>();
    s.start({ name: 'x', code: 'C1', version: 3 }, 3);
    expect(s.dirty.value).toBe(false);
    s.draft.value!.name = 'y';
    expect(s.dirty.value).toBe(true);
    s.draft.value!.name = 'x';
    expect(s.dirty.value).toBe(false);
  });
  it('rebase 到服务器最新版本后版本前移、草稿重置', () => {
    const s = useDraftSession<A>();
    s.start({ name: 'x', code: 'C1', version: 3 }, 3);
    s.draft.value!.name = 'mine';
    s.rebase({ name: 'theirs', code: 'C1', version: 4 }, 4);
    expect(s.baseVersion.value).toBe(4);
    expect(s.draft.value?.name).toBe('theirs');
    expect(s.dirty.value).toBe(false);
  });
  it('关闭后状态清空且不脏', () => {
    const s = useDraftSession<A>();
    s.start({ name: 'x', code: null, version: 1 }, 1);
    s.draft.value!.name = 'y';
    s.close();
    expect(s.open.value).toBe(false);
    expect(s.dirty.value).toBe(false);
    expect(s.baseVersion.value).toBeNull();
  });
});

describe('allowDiscard（DirtyGuard）', () => {
  it('干净草稿直接关闭，不弹确认', () => {
    let asked = false;
    expect(allowDiscard(false, () => { asked = true; return false; })).toBe(true);
    expect(asked).toBe(false);
  });
  it('脏草稿必须确认；拒绝则保留', () => {
    expect(allowDiscard(true, () => false)).toBe(false);
    expect(allowDiscard(true, () => true)).toBe(true);
  });
});

describe('fieldDiff（409 字段对比）', () => {
  it('只列出不同字段，使用中文标签；忽略指定字段', () => {
    const d = fieldDiff<A>({ name: '我的', code: 'C1', version: 3 }, { name: '他人', code: 'C1', version: 4 }, { name: '名称', code: '编号' }, ['version']);
    expect(d).toEqual([{ field: '名称', mine: '我的', theirs: '他人' }]);
  });
  it('未列入标签的字段变化也会报告（如后台状态变化），不会误报为无差异', () => {
    type S = { name: string; status: string; version: number };
    const d = fieldDiff<S>({ name: 'x', status: 'DRAFT', version: 1 }, { name: 'x', status: 'OUT_OF_SERVICE', version: 2 }, { name: '名称' }, ['version']);
    expect(d).toEqual([{ field: 'status', mine: 'DRAFT', theirs: 'OUT_OF_SERVICE' }]);
  });
  it('sameData 区分 null 与值', () => {
    expect(sameData(null, undefined)).toBe(true);
    expect(sameData('a', null)).toBe(false);
  });
});

describe('allowClose（提交中禁止关闭）', () => {
  it('提交中一律拒绝且不弹确认', () => {
    let asked = false;
    expect(allowClose(true, true, () => { asked = true; return true; })).toBe(false);
    expect(allowClose(false, true, () => { asked = true; return true; })).toBe(false);
    expect(asked).toBe(false);
  });
  it('非提交中按 DirtyGuard 规则', () => {
    expect(allowClose(false, false, () => false)).toBe(true);
    expect(allowClose(true, false, () => false)).toBe(false);
    expect(allowClose(true, false, () => true)).toBe(true);
  });
});

describe('allowRebase（基于最新版本重新编辑）', () => {
  it('草稿干净时无需确认', () => {
    let asked = false;
    expect(allowRebase(false, () => { asked = true; return false; })).toBe(true);
    expect(asked).toBe(false);
  });
  it('草稿有改动时必须确认；取消则草稿与版本不变', () => {
    const s = useDraftSession<A>();
    s.start({ name: 'x', code: 'C1', version: 3 }, 3);
    s.draft.value!.name = 'mine';
    if (allowRebase(s.dirty.value, () => false)) s.rebase({ name: 'theirs', code: 'C1', version: 4 }, 4);
    expect(s.draft.value?.name).toBe('mine');
    expect(s.baseVersion.value).toBe(3);
  });
  it('draftText 生成可复制的中文草稿文本，null 显示为清空', () => {
    expect(draftText<A>({ name: '新名', code: null }, { name: '名称', code: '编号' })).toBe('名称：新名\n编号：（清空）');
  });
});
