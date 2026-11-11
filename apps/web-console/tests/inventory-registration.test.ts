import { describe, expect, it } from 'vitest';
import {
  blankRegistration, validateRegistration, toRegistration, precisionText, selectedAdminCode, provinces, childrenOf,
  registrationErrorMessage, runtimeSourceText, verifyOutcome,
} from '../src/cloud/inventory-registration';
import { ApiError } from '../src/cloud/client';
import type { GeoManifest } from '../src/scene/geo-manifest';
import type { InventoryProject } from '../../../contracts/typescript/types.gen';

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
  it('错误提示只陈述响应能证明的事实：仅 400/422/行政区 503 可断言未受理', () => {
    const m = (st: number, msg = 'x') => registrationErrorMessage(new ApiError(st, 'X', msg));
    expect(m(422).provenUnwritten).toBe(true);
    expect(m(400).provenUnwritten).toBe(true);
    expect(m(503, 'administrative index unavailable')).toMatchObject({ provenUnwritten: true });
    for (const st of [401, 403, 404, 409, 503, 500]) {
      expect(m(st).provenUnwritten).toBe(false);
      expect(m(st).text).not.toMatch(/未做任何修改|已登记（服务端确认）/);
    }
    expect(registrationErrorMessage(new TypeError('fetch'))).toEqual({ text: '网络连接失败或请求超时，结果未知。', provenUnwritten: false });
  });
  it('只有可读取的 Inventory 记录（含 lifecycle）才算已登记；缺席或读取失败一律待核实', () => {
    const sub = { lifecycle: 'PLANNED' as const, adminCode: 330481, locationLabel: '', scheduleText: '', notes: '' };
    const inv = (over: Partial<InventoryProject> = {}): InventoryProject => ({ id: 'FP-1', name: 'n', lifecycle: 'PLANNED', province: '浙江省', city: '嘉兴市', county: '海宁市', locationLabel: '', adminCode: 330481, scheduleText: '', notes: '', assetDeviceCount: 0, runtimeDeviceCount: 0, realConnectedCount: 0, simulatedCount: 0, ...over });
    expect(verifyOutcome('FP-1', sub, null).kind).toBe('unverified');
    expect(verifyOutcome('FP-1', sub, []).kind).toBe('unverified');
    expect(verifyOutcome('FP-1', sub, [inv({ id: 'OTHER' })]).kind).toBe('unverified');
    expect(verifyOutcome('FP-1', sub, [inv()])).toMatchObject({ kind: 'registered', applied: 'same' });
    const other = verifyOutcome('FP-1', sub, [inv({ adminCode: 440300, province: '广东省', city: '深圳市', county: '' })]);
    expect(other).toMatchObject({ kind: 'registered', applied: 'different' });
    expect(other.text).toMatch(/广东省 深圳市.*本次表单内容未被应用/);
  });
  it('运行源只来自事实：无运行身份 ≠ 离线，模拟 ≠ 真实在线', () => {
    expect(runtimeSourceText({ runtimeDeviceCount: 0, realConnectedCount: 0, simulatedCount: 0 })).toBe('尚未接入运行源');
    expect(runtimeSourceText({ runtimeDeviceCount: 2, realConnectedCount: 0, simulatedCount: 2 })).toMatch(/非真实在线/);
    expect(runtimeSourceText({ runtimeDeviceCount: 2, realConnectedCount: 1, simulatedCount: 0 })).toBe('真实在线 1');
  });
});
