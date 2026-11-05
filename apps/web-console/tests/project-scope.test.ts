import { describe, it, expect } from 'vitest';
import { scopeFromQuery, isAuthorizedScope, projectLabel, scopeKey } from '../src/cloud/project-scope';

const grants = [
  { projectId: 'P001', projectName: '一号项目' },
  { projectId: 'P002', projectName: '二号项目' },
];

describe('项目范围解析', () => {
  it('缺失/非字符串/空白 query 均视为全部已授权项目', () => {
    expect(scopeFromQuery(undefined)).toBe('');
    expect(scopeFromQuery(['P001'])).toBe('');
    expect(scopeFromQuery('  ')).toBe('');
    expect(scopeFromQuery('P001')).toBe('P001');
  });
  it('空范围总是合法；授权项目合法；未知/未授权项目不合法', () => {
    expect(isAuthorizedScope('', grants)).toBe(true);
    expect(isAuthorizedScope('P001', grants)).toBe(true);
    expect(isAuthorizedScope('P999', grants)).toBe(false);
    expect(isAuthorizedScope('P001', [])).toBe(false);
  });
  it('项目名只取授权映射，取不到时回落到 ID，不虚构名称', () => {
    expect(projectLabel('P001', grants)).toBe('一号项目');
    expect(projectLabel('P999', grants)).toBe('P999');
  });
  it('缓存键随范围区分，P001 与 P002、全部 三者互不复用', () => {
    const keys = new Set([scopeKey(''), scopeKey('P001'), scopeKey('P002')]);
    expect(keys.size).toBe(3);
    expect(scopeKey('')).not.toBe('');
  });
});
