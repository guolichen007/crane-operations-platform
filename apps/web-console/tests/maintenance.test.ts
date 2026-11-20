import { describe, expect, it } from 'vitest';
import { sourceModeBadge, statusView, daysLeftText, formatNumber, groupByMechanism, filterCenter, type CenterRow } from '../src/cloud/maintenance';

describe('R9 maintenance presentation', () => {
  it('marks simulated and test data loudly; real data is plain', () => {
    expect(sourceModeBadge('SIMULATION')).toEqual({ text: '模拟数据', tone: 'sim' });
    expect(sourceModeBadge('TEST')).toEqual({ text: '测试数据', tone: 'sim' });
    expect(sourceModeBadge('REAL')).toEqual({ text: '真实', tone: 'real' });
    expect(sourceModeBadge(null).text).toBe('无数据');
  });
  it('maps every schedule status, never inventing a due date', () => {
    expect(statusView('INSUFFICIENT_DATA').text).toBe('数据不足');
    expect(statusView('OVERDUE').tone).toBe('over');
    expect(statusView('DUE_SOON').text).toBe('即将到期');
    expect(daysLeftText(null)).toBe('—');
    expect(daysLeftText(0)).toBe('今日到期');
    expect(daysLeftText(-3)).toBe('逾期 3 天');
    expect(daysLeftText(30)).toBe('剩余 30 天');
  });
  it('formats counts and units', () => {
    expect(formatNumber(1000000, 'count')).toBe('1,000,000 次');
    expect(formatNumber(103.016667, 'h')).toBe('103.017 小时'); // R9-UX: Chinese business units
    expect(formatNumber(null)).toBe('—');
  });
  it('groups points by mechanism in source order', () => {
    const g = groupByMechanism([{ mechanism: '大车机构', id: 1 }, { mechanism: '小车机构', id: 2 }, { mechanism: '大车机构', id: 3 }]);
    expect(g.map((x) => [x.mechanism, x.rows.length])).toEqual([['大车机构', 2], ['小车机构', 1]]);
  });
  it('center filters: REAL by default excludes simulation; every dimension narrows', () => {
    const row = (over: Partial<CenterRow>): CenterRow => ({ projectId: 'LHK', projectName: '老河口', assetId: 'A', assetName: 'A', assetCode: 'A', mechanism: '大车机构', equipmentSlot: 'GANTRY-01',
      equipmentType: 'CONTACTOR', category: 'REPLACEMENT_DUE', ruleId: 'r', ruleName: 'r', approvalStatus: 'APPROVED', sourceMode: 'REAL', status: 'DUE', nextDueAt: null,
      progressPercent: null, daysLeft: null, openWorkOrders: 0, ...over });
    const rows = [row({}), row({ sourceMode: 'SIMULATION' }), row({ mechanism: '小车机构', equipmentSlot: 'TROLLEY-14' }), row({ status: 'NORMAL' })];
    const base = { project: '', asset: '', mechanism: '', slot: '', status: '', mode: 'REAL' as const };
    expect(filterCenter(rows, base)).toHaveLength(3);
    expect(filterCenter(rows, { ...base, mode: 'SIM' })).toHaveLength(1);
    expect(filterCenter(rows, { ...base, mechanism: '小车机构' })).toHaveLength(1);
    expect(filterCenter(rows, { ...base, status: 'DUE' })).toHaveLength(2);
    expect(filterCenter(rows, { ...base, project: 'OTHER' })).toHaveLength(0);
  });
});
