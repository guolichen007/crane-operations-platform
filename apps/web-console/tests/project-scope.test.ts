import { describe, it, expect } from 'vitest';
import { scopeFromQuery, isAuthorizedScope, projectLabel, scopeKey, scopeState, scopeSupported, scopeMode, ledgerQueryFrom, ledgerReturnQuery } from '../src/cloud/project-scope';

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

describe('列表请求门禁（scopeState）', () => {
  it('生产认证下会话未加载：不请求、不显示空态', () => {
    expect(scopeState(true, null, '')).toBe('session-loading');
    expect(scopeState(true, null, 'P001')).toBe('session-loading');
  });
  it('未知/未授权项目：不请求', () => {
    expect(scopeState(true, grants, 'P999')).toBe('unauthorized');
    expect(scopeState(true, [], 'P001')).toBe('unauthorized');
  });
  it('授权项目或全部：可请求', () => {
    expect(scopeState(true, grants, 'P001')).toBe('ready');
    expect(scopeState(true, grants, '')).toBe('ready');
    expect(scopeState(true, [], '')).toBe('ready');
  });
  it('开发模式（无 OIDC 授权）不等待会话', () => {
    expect(scopeState(false, null, 'P001')).toBe('ready');
  });
});

describe('顶栏项目范围适用页面', () => {
  it('真正按 ?project= 过滤的页面显示选择器', () => {
    for (const p of ['/overview', '/projects', '/devices', '/assets', '/locations']) expect(scopeSupported(p)).toBe(true);
  });
  it('不支持过滤的页面不显示带误导的项目选择', () => {
    for (const p of ['/portfolio', '/runtime', '/alarms', '/catalog', '/assets/abc']) expect(scopeSupported(p)).toBe(false);
  });
});

describe('scopeMode（顶栏语义）', () => {
  it('总览是聚焦而非过滤；台账类为过滤；其余无', () => {
    expect(scopeMode('/overview')).toBe('focus');
    for (const p of ['/projects', '/devices', '/assets', '/locations']) expect(scopeMode(p)).toBe('filter');
    for (const p of ['/catalog', '/runtime', '/assets/abc']) expect(scopeMode(p)).toBe('none');
  });
});

describe('台账返回上下文', () => {
  const grants = [{ projectId: 'P001', projectName: 'A' }];
  it('只保留已知键与非空值', () => {
    expect(ledgerQueryFrom({ project: 'P001', q: ' x ', page: '3', evil: 'y', status: '' })).toEqual({ project: 'P001', q: 'x', page: '3' });
  });
  it('合法项目范围、搜索与页码原样恢复', () => {
    expect(ledgerReturnQuery(grants, { project: 'P001', q: 'x', page: '3' })).toEqual({ project: 'P001', q: 'x', page: '3' });
  });
  it('未授权项目被丢弃，连同其页码；搜索条件保留', () => {
    expect(ledgerReturnQuery(grants, { project: 'P999', q: 'x', page: '3' })).toEqual({ q: 'x' });
  });
  it('会话未知时不携带项目范围', () => {
    expect(ledgerReturnQuery(null, { project: 'P001', page: '2' })).toEqual({});
  });
});
