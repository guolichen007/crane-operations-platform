import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { ApiError, NetworkWriteError } from '../src/cloud/client';
import {
  EQUIPMENT_TYPE_TEXT, equipmentTypeName, errorZh, historySummaryZh, meterMethodZh, pendingValueZh, unitZh, zh, ASSET_STATUS_ZH, PRODUCT_NAME,
} from '../src/cloud/zh';

describe('R9-UX 中文展示模块', () => {
  it('51 类设备类型与后端代码注册表一一对应（同一中文名）', () => {
    const go = readFileSync(resolve(__dirname, '../../../internal/equipment/types.go'), 'utf8');
    const reg = [...go.matchAll(/\{"([A-Z0-9_]+)", "([^"]+)", "[A-Z]+"\}/g)].map((m) => [m[1], m[2]]);
    expect(reg.length).toBe(51);
    expect(Object.keys(EQUIPMENT_TYPE_TEXT).length).toBe(51);
    for (const [code, name] of reg) expect(EQUIPMENT_TYPE_TEXT[code], code).toBe(name);
  });
  it('未知枚举不直接显示程序编码', () => {
    expect(zh(ASSET_STATUS_ZH, 'SOMETHING_NEW')).toBe('未知');
    expect(equipmentTypeName('NEW_TYPE_X')).toBe('未登记的设备类型');
    expect(zh(ASSET_STATUS_ZH, null)).toBe('—');
  });
  it('单位、计算说明、待确认值为中文，未确认点位只显示候选口径', () => {
    expect(unitZh('count')).toBe('次');
    expect(unitZh('h')).toBe('小时');
    const method = meterMethodZh({ metric: 'cycles', kind: 'CUMULATIVE', formula: 'edge-counted rising edges of the raw bit' }, false);
    expect(method).toMatch(/^候选口径（待现场确认）/);
    expect(method).not.toMatch(/edge|rising|raw/i);
    expect(meterMethodZh({ metric: 'cycles', kind: 'CUMULATIVE' }, true)).not.toMatch(/候选/);
    expect(meterMethodZh({ metric: 'hours', kind: 'CUMULATIVE', formula: 'PROXY: hoist brake' }, true)).toMatch(/代理量/);
    expect(pendingValueZh('rfidEntity', 'RFID_READER')).toBe('RFID 读写器');
    expect(pendingValueZh('ratedCapacityT', 10)).toBe('10 吨');
  });
  it('历史结构化事件转中文，不改原记录', () => {
    expect(historySummaryZh('WORK_ORDER', 'SIM-WO-20261120-ABC123 ASSIGNED→IN_PROGRESS（开始）')).toBe('SIM-WO-20261120-ABC123 已分配 → 执行中（开始）');
    expect(historySummaryZh('WORK_ORDER', 'WO-1 →OPEN（from alert PCT_100）')).toContain('创建 → 待分配');
    expect(historySummaryZh('LEDGER', 'equipment installedAt')).toBe('器件 · 实际安装日期');
    expect(historySummaryZh('LEDGER', 'project created')).toBe('项目 · 新建');
    expect(historySummaryZh('WIRE_ROPE', 'PERIODIC CONTINUE_MONITOR：局部磨损')).toBe('定期检查 · 继续使用并加强监测：局部磨损');
    expect(historySummaryZh('FAULT', 'SIM_TEST_FAULT 模拟故障验收')).toBe('模拟故障（验收用）：模拟故障验收（故障码 SIM_TEST_FAULT）');
  });
  it('错误消息中文；英文诊断不作为主提示；结果未知不说成失败', () => {
    const cases: [ApiError, RegExp][] = [
      [new ApiError(401, 'UNKNOWN_ERROR', 'unauthorized'), /重新登录/],
      [new ApiError(403, 'NO_PERMISSION', 'Forbidden'), /权限/],
      [new ApiError(404, 'NOT_FOUND', 'not found'), /不存在|无权/],
      [new ApiError(409, 'VERSION_CONFLICT', 'conflict: the point changed (version 3)'), /他人修改/],
      [new ApiError(422, 'VALIDATION_ERROR', 'invalid input: a counted bit point needs the counted edge'), /计数边沿/],
      [new ApiError(422, 'VALIDATION_ERROR', 'invalid input: something brand new'), /输入不符合要求/],
      [new ApiError(429, 'UNKNOWN_ERROR', 'too many'), /频繁/],
      [new ApiError(503, 'DATABASE_UNAVAILABLE', 'database unavailable'), /暂时不可用/],
      [new ApiError(409, 'NOT_TEST_ACCOUNT', 'x'), /测试账号/],
    ];
    for (const [e, re] of cases) {
      const m = errorZh(e);
      expect(m, e.message).toMatch(re);
      expect(m, e.message).not.toMatch(/[A-Za-z]{4,}/);
    }
    expect(errorZh(new NetworkWriteError(true))).toMatch(/结果未知/);
    expect(errorZh(new NetworkWriteError(false))).toMatch(/未发送/);
    expect(errorZh(new TypeError('Failed to fetch'), 'load')).toMatch(/网络/);
  });
  it('产品名称统一', () => expect(PRODUCT_NAME).toBe('友道智造 · 行车综合管理平台'));
});
