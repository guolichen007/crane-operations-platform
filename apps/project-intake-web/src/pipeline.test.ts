import { describe, it, expect } from 'vitest';
import { nextStages, followupState, columnQuery, stageCodes } from './pipeline';

describe('销售管道规则镜像', () => {
  it('12 个阶段顺序与服务端一致', () => {
    expect(stageCodes).toEqual(['L0_LEAD', 'L1_REQUIREMENT', 'L2_TECHNICAL', 'L3_SITE_SURVEY', 'L4_SOLUTION', 'L5_QUOTATION', 'L6_TENDER', 'L7_COMMERCIAL', 'L8_PENDING_CONTRACT', 'WON', 'LOST', 'HOLD']);
  });
  it('已提交项目只能逐步前进、暂停或失单，待签约才能成交', () => {
    expect(nextStages('L2_TECHNICAL', 'SUBMITTED')).toEqual(['L3_SITE_SURVEY', 'HOLD', 'LOST']);
    expect(nextStages('L8_PENDING_CONTRACT', 'APPROVED')).toEqual(['WON', 'HOLD', 'LOST']);
    expect(nextStages('HOLD', 'UNDER_REVIEW')).toEqual(['L1_REQUIREMENT']);
    expect(nextStages('WON', 'APPROVED')).toEqual([]);
    expect(nextStages('LOST', 'APPROVED')).toEqual([]);
  });
  it('草稿与退回可改为任意其他阶段，已结束报备不可改', () => {
    expect(nextStages('L0_LEAD', 'DRAFT')).toHaveLength(11);
    expect(nextStages('WON', 'RETURNED')).toHaveLength(11);
    expect(nextStages('L1_REQUIREMENT', 'REJECTED')).toEqual([]);
  });
});

describe('跟进状态只看真实下次跟进时间', () => {
  const asOf = '2026-11-09T00:00:00Z';
  it('未安排、已逾期、已安排', () => {
    expect(followupState(null, asOf)).toBe('unscheduled');
    expect(followupState('', asOf)).toBe('unscheduled');
    expect(followupState('2026-11-08T23:59:59Z', asOf)).toBe('overdue');
    expect(followupState('2026-11-09T00:00:00Z', asOf)).toBe('scheduled');
  });
});

describe('栏请求参数', () => {
  it('基础筛选、阶段与分页', () => {
    expect(columnQuery({ q: '甲', status: '', province: '江苏省', updatedAfter: '' }, 'L3_SITE_SURVEY', 2, 10)).toBe(
      'q=%E7%94%B2&province=%E6%B1%9F%E8%8B%8F%E7%9C%81&stage=L3_SITE_SURVEY&page=2&pageSize=10'
    );
  });
});
