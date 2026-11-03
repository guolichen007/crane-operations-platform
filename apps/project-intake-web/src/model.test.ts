import { describe, it, expect } from 'vitest';
import { blank, stages, statuses, reqs } from './model';
describe('报备编辑模型', () => {
  it('独立草稿数组与地点不会串改', () => {
    const a = blank(),
      b = blank();
    a.location.city = '合成市';
    a.requirements.push('SAFETY');
    expect(b.location.city).toBe('');
    expect(b.requirements).toEqual([]);
  });
  it('状态与阶段分离，不存在正式设备身份', () => {
    const p = blank();
    expect(stages[p.salesStage]).toBe('线索');
    expect(statuses.DRAFT).toBe('草稿');
    expect(reqs.ANTI_SWAY).toBe('防摇');
    expect(p).not.toHaveProperty('serialNumber');
    expect(p).not.toHaveProperty('runtimeBinding');
  });
});
