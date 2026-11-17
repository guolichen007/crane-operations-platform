import { describe, expect, it } from 'vitest';
import { readFilters, returnLabel, safeReturn, scopeParams, toList, toPipeline, writeFilters } from './view-state';

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
    expect(returnLabel('/projects', '项目中心')).toBe('返回项目中心');
  });
});

describe('V1.1 scope and group in the URL', () => {
  const id = '11111111-1111-4111-8111-111111111111';
  it('keeps only valid scope, member, team and group values', () => {
    const f = readFilters({ scope: 'member', memberId: id, teamId: 'x', group: 'G3' });
    expect(f).toMatchObject({ scope: 'member', memberId: id, teamId: '', group: 'G3' });
    expect(readFilters({ scope: 'everyone', memberId: '1 OR 1=1', group: 'G9' })).toMatchObject({ scope: '', memberId: '', group: '' });
  });
  it('writes the member only with scope=member and the team only with scope=team', () => {
    expect(writeFilters({ scope: 'team', memberId: id, teamId: id })).toEqual({ scope: 'team', teamId: id });
    expect(scopeParams({ scope: 'member', memberId: id, teamId: id })).toEqual({ scope: 'member', memberId: id });
    expect(scopeParams({ scope: '', memberId: id, teamId: '' })).toEqual({});
  });
  it('carries the scope between list and pipeline', () => {
    const f = readFilters({ scope: 'mine', q: 'a' });
    expect(toPipeline(f)).toEqual({ q: 'a', scope: 'mine' });
    expect(toList(f)).toEqual({ q: 'a', scope: 'mine' });
  });
});

describe('V1.1 customer return paths', () => {
  it('accepts a customer detail page as origin, nothing else under /customers', () => {
    const id = '11111111-1111-4111-8111-111111111111';
    expect(safeReturn('/customers/' + id + '?from=/customers')).toBe('/customers/' + id + '?from=/customers');
    expect(safeReturn('/customers/../settings/users')).toBe('/projects');
    expect(returnLabel('/customers/' + id, '项目中心')).toBe('返回客户详情');
    expect(returnLabel('/customers?q=a', '项目中心')).toBe('返回客户中心');
  });
});
