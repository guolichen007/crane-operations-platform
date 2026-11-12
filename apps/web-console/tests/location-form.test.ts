import { describe, expect, it } from 'vitest';
import { blankLocation, locationErrorText, toNewLocation, validateLocation } from '../src/cloud/location-form';
import { ApiError } from '../src/cloud/client';

describe('位置创建表单（Q4-QA-R1）', () => {
  it('类型与名称必填；超长输入在提交前提示', () => {
    expect(Object.keys(validateLocation(blankLocation())).sort()).toEqual(['kind', 'name']);
    expect(validateLocation({ ...blankLocation(), kind: 'SITE', name: '  ' }).name).toBeTruthy();
    expect(validateLocation({ ...blankLocation(), kind: 'SITE', name: '一号场地' })).toEqual({});
    expect(validateLocation({ ...blankLocation(), kind: 'SITE', name: 'x', code: 'c'.repeat(161) }).code).toBeTruthy();
  });
  it('请求体不编造坐标：经纬度为空、坐标系 UNKNOWN；空编号/上级为 null', () => {
    const b = toNewLocation('P001', { kind: 'WORKSHOP', name: ' 一车间 ', code: ' ', parentId: '', address: '' });
    expect(b).toMatchObject({ projectId: 'P001', kind: 'WORKSHOP', name: '一车间', code: null, parentId: null, longitude: null, latitude: null, coordinateSystem: 'UNKNOWN', status: 'ACTIVE' });
  });
  it('错误提示区分 422/409/403/503/网络，且 503 与网络不声称“未创建”', () => {
    const m = (s: number) => locationErrorText(new ApiError(s, 'X', 'name is required'));
    expect(m(422)).toMatch(/未创建：name is required/);
    expect(m(409)).toMatch(/编号.*已存在/);
    expect(m(403)).toMatch(/权限/);
    expect(m(503)).not.toMatch(/未创建/);
    expect(locationErrorText(new TypeError('fetch'))).not.toMatch(/未创建/);
  });
});
