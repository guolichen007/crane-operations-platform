import { describe, it, expect } from 'vitest';
import { groupDecimal, yuan, sharePercent } from './amount';

describe('金额十进制字符串格式化（不经 JS number）', () => {
  it('千位分隔与两位小数', () => {
    expect(groupDecimal('0.00')).toBe('0.00');
    expect(groupDecimal('0.01')).toBe('0.01');
    expect(groupDecimal('0.10')).toBe('0.10');
    expect(groupDecimal('125000.25')).toBe('125,000.25');
    expect(groupDecimal('1000.00')).toBe('1,000.00');
  });
  it('超过 JS 安全整数的金额不丢位', () => {
    // 2^53 = 9007199254740992; this value in cents is far above it.
    expect(groupDecimal('100000125251.13')).toBe('100,000,125,251.13');
    expect(groupDecimal('999999999999999999.99')).toBe('999,999,999,999,999,999.99');
    expect(Number('100000125251.13').toFixed(2)).toBe('100000125251.13'); // control: fine here
    expect(groupDecimal('90071992547409.91')).toBe('90,071,992,547,409.91');
  });
  it('非法输入不显示为 0', () => {
    for (const bad of ['', '12', '12.5', '1e5', '-1.00', 'NaN', '1,000.00']) expect(groupDecimal(bad)).toBeNull();
    expect(yuan('12.5')).toBe('金额格式异常');
    expect(yuan('125000.25')).toBe('¥125,000.25');
  });
  it('占比只用于条形宽度，用整数分计算', () => {
    expect(sharePercent('25.00', '100.00')).toBe(25);
    expect(sharePercent('0.00', '0.00')).toBe(0);
    expect(sharePercent('1.00', '3.00')).toBe(33.33);
  });
});
