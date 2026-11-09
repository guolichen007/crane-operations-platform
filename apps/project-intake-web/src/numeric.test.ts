import { describe, it, expect } from 'vitest';
import { parseMoney } from './numeric';

const ok = (raw: string) => {
  const r = parseMoney(raw);
  return 'value' in r ? r.value : r.error;
};
describe('预估金额输入精确到分（与服务端 checkAmountToken 一致）', () => {
  it('合法金额原样接受，空为未知', () => {
    expect(ok('')).toBeNull();
    expect(ok('0')).toBe(0);
    expect(ok('0.01')).toBe(0.01);
    expect(ok('0.10')).toBe(0.1);
    expect(ok('125000.25')).toBe(125000.25);
    expect(ok('125,000.25')).toBe(125000.25);
    expect(ok('1.500')).toBe(1.5);
    expect(ok('9999999999999.99')).toBe(9999999999999.99);
    expect(ok('90071992547409.90')).toBe(90071992547409.9);
    expect(ok('100000000000000')).toBe(100000000000000);
  });
  it('超过两位小数、超限、格式错误与无法精确发送的金额明确拒绝', () => {
    expect(ok('0.125')).toContain('最多保留两位小数');
    expect(ok('100.005')).toContain('最多保留两位小数');
    expect(ok('12345678.9999')).toContain('最多保留两位小数');
    expect(ok('123.456')).toContain('最多保留两位小数');
    expect(ok('90071992547409.91')).toContain('无法精确保存到分');
    expect(ok('99999999999999.99')).toContain('无法精确保存到分');
    expect(ok('100000000000000.01')).toContain('超出允许范围');
    expect(ok('-1')).toContain('不小于 0');
    expect(ok('1e3')).toContain('不小于 0');
    expect(ok('abc')).toContain('不小于 0');
    expect(ok('01')).toContain('不小于 0');
  });
});
