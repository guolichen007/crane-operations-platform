import { describe, expect, it } from 'vitest';
import { assetCodeDisplay, RUNTIME_LINK_LABEL } from '../src/cloud/format';

describe('R5-UX-PERF D 资产识别与运行入口措辞', () => {
  it('有业务编号时原样显示（去首尾空白）', () => {
    expect(assetCodeDisplay({ id: 'a-1', assetCode: ' HN-QD-0001 ' })).toEqual({ code: 'HN-QD-0001', missing: false, internalId: 'a-1' });
  });
  it('空编号不伪造：标记待补全并给出内部资产 ID', () => {
    for (const assetCode of [null, undefined, '', '   ']) {
      const d = assetCodeDisplay({ id: 'b-2', assetCode });
      expect(d.code).toBeNull();
      expect(d.missing).toBe(true);
      expect(d.internalId).toBe('b-2');
    }
  });
  it('运行身份入口不暗示当前在线', () => {
    expect(RUNTIME_LINK_LABEL).toBe('查看运行状态');
    expect(RUNTIME_LINK_LABEL).not.toMatch(/LIVE|在线|实时/);
  });
});
