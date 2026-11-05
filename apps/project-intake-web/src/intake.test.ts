import { describe, it, expect } from 'vitest';
import { parseNumber, consistent, isLegacyZero } from './numeric';
import { readIntent, startIntent, markSent, clearIntent } from './create-intent';

describe('数值已知性', () => {
  it('空白为未知，非法输入不覆盖', () => {
    expect(parseNumber('')).toBeNull();
    expect(parseNumber(' 32.5 ')).toBe(32.5);
    expect(parseNumber('-1')).toBeUndefined();
    expect(parseNumber('1.5', true)).toBeUndefined();
  });
  it('历史 0 不会被静默确认，未知不会写成 0', () => {
    expect(consistent({ a: 'LEGACY_UNCONFIRMED' }, { a: 0 })).toEqual({ a: 'LEGACY_UNCONFIRMED' });
    expect(consistent({ a: 'LEGACY_UNCONFIRMED' }, { a: 5 })).toEqual({ a: 'KNOWN' });
    expect(consistent({ a: 'KNOWN' }, { a: null })).toEqual({ a: 'UNKNOWN' });
    expect(consistent(undefined, { a: 0 })).toEqual({ a: 'KNOWN' });
    expect(isLegacyZero({ a: 'LEGACY_UNCONFIRMED' }, 'a', 0)).toBe(true);
  });
});

describe('创建意图', () => {
  const memory = () => {
    const m = new Map<string, string>();
    return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v), removeItem: (k: string) => void m.delete(k) };
  };
  it('按账号隔离、跨刷新保留已发送标记、过期清除', () => {
    const s = memory();
    const v = startIntent('actor-a', s, 1000);
    expect(v.key).toMatch(/^[A-Za-z0-9_-]{16,128}$/);
    markSent(v, s);
    expect(readIntent('actor-a', s, 2000)).toMatchObject({ key: v.key, sent: true });
    expect(readIntent('actor-b', s, 2000)).toBeNull();
    const w = startIntent('actor-a', s, 0);
    expect(readIntent('actor-a', s, 9 * 3600_000)).toBeNull();
    void w;
    clearIntent(s);
    expect(readIntent('actor-a', s, 0)).toBeNull();
  });
});
