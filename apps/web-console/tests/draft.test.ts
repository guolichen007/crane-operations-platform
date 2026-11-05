import { describe, it, expect } from 'vitest';
import { useDraftSession, allowDiscard, fieldDiff, sameData } from '../src/cloud/draft';

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
  it('只列出不同字段，使用中文标签', () => {
    const d = fieldDiff<A>({ name: '我的', code: 'C1', version: 3 }, { name: '他人', code: 'C1', version: 4 }, { name: '名称', code: '编号' });
    expect(d).toEqual([{ field: '名称', mine: '我的', theirs: '他人' }]);
  });
  it('sameData 区分 null 与值', () => {
    expect(sameData(null, undefined)).toBe(true);
    expect(sameData('a', null)).toBe(false);
  });
});
