// H2-1D-2: API-mode KPI / Attention / region numbers are summary fields, nothing else.
import { describe, it, expect } from 'vitest';
import { summaryKpis, summaryAttention, regionPartition } from '../src/scene/home-summary';
import { homeKpis } from '../src/scene/home-metrics';
import { summary } from './summary-fixtures';

const value = (k: ReturnType<typeof summaryKpis>, id: string) => k.find((x) => x.id === id)!.value;

describe('summary KPIs', () => {
  it('five cards, each exactly one summary field', () => {
    const s = summary({ registered: 37, realConnected: 5, alarmReal: 2, pendingReview: 3, noRuntime: 11, boundReal: 9, boundSim: 20 });
    const k = summaryKpis(s);
    expect(k.map((x) => x.id)).toEqual(['registered', 'real-connected', 'alarm-real', 'pending-review', 'no-runtime']);
    expect(k.map((x) => x.value)).toEqual([37, 5, 2, 3, 11]);
    expect(k.find((x) => x.id === 'real-connected')!.label).toBe('REAL 在线');
    expect(k.some((x) => x.label.includes('真实运行'))).toBe(false);
  });
  it('REAL 在线 is metrics.real.connected — SIM online / SIM alarm never count', () => {
    const s = summary({ registered: 2, boundReal: 1, boundSim: 1, realConnected: 0, offline: 1, alarmSim: 1 });
    const k = summaryKpis(s);
    expect(value(k, 'real-connected')).toBe(0);
    expect(value(k, 'alarm-real')).toBe(0);
  });
  it('current alarm 0 with 150 historical records: the card is 0, the window total is 150', () => {
    const k = summaryKpis(summary({ alarmReal: 0, recordsTotal: 150 }));
    expect(value(k, 'alarm-real')).toBe(0);
    expect(k.find((x) => x.id === 'alarm-real')!.detail).toBe('近 24 小时告警记录 150 条（全部来源）');
    expect(summaryKpis(summary({ recordsTotal: 150, window: 'P7D' })).find((x) => x.id === 'alarm-real')!.detail).toContain('近 7 天');
  });
  it('no summary → every value null (rendered as —/不可用, never 0)', () => {
    expect(summaryKpis(null).every((x) => x.value === null)).toBe(true);
    expect(summaryAttention(null).every((x) => x.value === null)).toBe(true);
  });
  it('no authorized project → legal zeros from a successful response', () => {
    const k = summaryKpis(summary({ authorizedProjects: 0, projects: 0 }));
    expect(k.every((x) => x.value === 0)).toBe(true);
  });
  it('demo KPIs are unchanged and tagged DEMO (demo never uses the summary)', () => {
    expect(homeKpis('demo', null, null, 0).filter((k) => k.id !== 'planned').every((k) => k.tag === 'DEMO')).toBe(true);
  });
});

describe('summary attention', () => {
  it('independent server fields, listed separately (never summed)', () => {
    const rows = summaryAttention(summary({ offline: 4, stale: 2, unknownTime: 1, pendingReviewAlarm: 1, unlocated: 6 }));
    expect(Object.fromEntries(rows.map((r) => [r.id, r.value]))).toEqual({
      'pending-review-alarm': 1,
      'real-offline': 4,
      'real-stale': 2,
      'real-unknown-time': 1,
      unlocated: 6,
    });
    expect(rows.some((r) => /合计|总计/.test(r.label))).toBe(false);
  });
});

describe('region partition', () => {
  it('children need not add up: Σ children + scope level + unlocated = registered', () => {
    const p = regionPartition(
      summary({
        registered: 24,
        unlocated: 2,
        atScopeLevel: 1,
        regions: [
          { adcode: 330000, name: '浙江省', registered: 20, realConnected: 3, realAlarmDevices: 1 },
          { adcode: 110000, name: '北京市', registered: 1, realConnected: 1, realAlarmDevices: 0 },
        ],
      })
    );
    expect(p).toEqual({ children: 21, atScopeLevel: 1, unlocated: 2, registered: 24 });
    expect(p.children + p.atScopeLevel + p.unlocated).toBe(p.registered);
  });
});
