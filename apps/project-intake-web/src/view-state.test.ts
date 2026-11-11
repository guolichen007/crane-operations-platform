import { describe, expect, it } from 'vitest';
import { readFilters, returnLabel, safeReturn, toList, toPipeline, writeFilters } from './view-state';

describe('view filters in the URL', () => {
  it('round-trips non-default filters and drops defaults', () => {
    const f = readFilters({ q: '西川', status: 'APPROVED', stage: 'L2_TECHNICAL', province: '江苏', after: '2026-11-01', sort: 'name', page: '3' });
    expect(writeFilters(f)).toEqual({ q: '西川', status: 'APPROVED', stage: 'L2_TECHNICAL', province: '江苏', after: '2026-11-01', sort: 'name', page: '3' });
    expect(writeFilters(readFilters({}))).toEqual({});
  });
  it('rejects malformed values', () => {
    const f = readFilters({ after: '11/01/2026', page: '-4', sort: 'evil' });
    expect(f.after).toBe('');
    expect(f.page).toBe(1);
    expect(f.sort).toBe('updated');
  });
  it('keeps shared filters when switching views', () => {
    const f = readFilters({ q: 'x', status: 'SUBMITTED', stage: 'L3_SITE_SURVEY', province: 'p', after: '2026-01-02', sort: 'name', page: '5' });
    expect(toPipeline(f)).toEqual({ q: 'x', status: 'SUBMITTED', province: 'p', after: '2026-01-02' });
    expect(toList(f)).toEqual({ q: 'x', status: 'SUBMITTED', province: 'p', after: '2026-01-02', sort: 'name' });
  });
});

describe('detail return target', () => {
  it('accepts only known local pages', () => {
    expect(safeReturn('/pipeline?q=a')).toBe('/pipeline?q=a');
    expect(safeReturn('/projects?status=APPROVED&page=2')).toBe('/projects?status=APPROVED&page=2');
    for (const bad of ['https://evil.test/', '//evil.test', '/settings/users', 'javascript:alert(1)', 42, undefined])
      expect(safeReturn(bad)).toBe('/projects');
  });
  it('names the return action after its target', () => {
    expect(returnLabel('/pipeline?q=a', '我的报备')).toBe('返回项目管道');
    expect(returnLabel('/projects', '我的报备')).toBe('返回我的报备');
  });
});
