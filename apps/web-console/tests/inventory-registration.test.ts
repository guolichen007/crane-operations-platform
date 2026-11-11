import { describe, expect, it } from 'vitest';
import {
  blankRegistration, validateRegistration, toRegistration, precisionText, selectedAdminCode, provinces, childrenOf,
  registrationErrorMessage, runtimeSourceText,
} from '../src/cloud/inventory-registration';
import { ApiError } from '../src/cloud/client';
import type { GeoManifest } from '../src/scene/geo-manifest';

const entry = (adcode: number, parent: number, level: string, name: string) => ({ adcode, parent, level, name, center: null, childrenFile: null, boundaryFile: null });
const m: GeoManifest = {
  '330000': entry(330000, 100000, 'province', '浙江省'),
  '330400': entry(330400, 330000, 'prefecture', '嘉兴市'),
  '330481': entry(330481, 330400, 'county', '海宁市'),
  '440000': entry(440000, 100000, 'province', '广东省'),
  '440300': entry(440300, 440000, 'prefecture', '深圳市'),
};

describe('Inventory 登记表单', () => {
  it('行政区由清单逐级选择，层级不一致被拒绝', () => {
    expect(provinces(m).map((r) => r.name)).toEqual(['浙江省', '广东省']);
    expect(childrenOf(m, '330000').map((r) => r.adcode)).toEqual([330400]);
    const ok = { ...blankRegistration(), lifecycle: 'PLANNED' as const, province: '330000', prefecture: '330400', county: '330481' };
    expect(validateRegistration(ok, m)).toEqual({});
    expect(validateRegistration({ ...ok, prefecture: '440300' }, m).prefecture).toMatch(/不匹配/);
    expect(validateRegistration({ ...ok, county: '440300' }, m).county).toMatch(/不匹配/);
    expect(validateRegistration({ ...blankRegistration() }, m)).toMatchObject({ lifecycle: expect.any(String), province: expect.any(String) });
    expect(validateRegistration(ok, undefined).province).toMatch(/未加载/);
  });
  it('提交最细一级编码，不含坐标或手填名称；精度如实说明', () => {
    const f = { ...blankRegistration(), lifecycle: 'COMPLETED' as const, province: '330000', prefecture: '330400' };
    expect(selectedAdminCode(f)).toBe(330400);
    expect(toRegistration(f)).toEqual({ lifecycle: 'COMPLETED', adminCode: 330400, locationLabel: '', scheduleText: '', notes: '' });
    expect(precisionText(m, f)).toBe('浙江省 嘉兴市（地市级行政区定位，非设备精确坐标）');
    expect(precisionText(m, { ...f, county: '330481' })).toContain('区县级');
    expect(precisionText(undefined, f)).toBe('尚未选择行政区');
  });
  it('错误提示区分权限、冲突、行政区不可用与网络失败', () => {
    expect(registrationErrorMessage(new ApiError(403, 'NO_PERMISSION', 'x'))).toMatch(/项目管理员/);
    expect(registrationErrorMessage(new ApiError(404, 'NOT_FOUND', 'x'))).toMatch(/尚未批准/);
    expect(registrationErrorMessage(new ApiError(409, 'VERSION_CONFLICT', 'x'))).toMatch(/已登记/);
    expect(registrationErrorMessage(new ApiError(503, 'DATABASE_UNAVAILABLE', 'administrative index unavailable'))).toMatch(/行政区数据/);
    expect(registrationErrorMessage(new TypeError('fetch'))).toMatch(/确认是否已登记/);
  });
  it('运行源只来自事实：无运行身份 ≠ 离线，模拟 ≠ 真实在线', () => {
    expect(runtimeSourceText({ runtimeDeviceCount: 0, realConnectedCount: 0, simulatedCount: 0 })).toBe('尚未接入运行源');
    expect(runtimeSourceText({ runtimeDeviceCount: 2, realConnectedCount: 0, simulatedCount: 2 })).toMatch(/非真实在线/);
    expect(runtimeSourceText({ runtimeDeviceCount: 2, realConnectedCount: 1, simulatedCount: 0 })).toBe('真实在线 1');
  });
});
