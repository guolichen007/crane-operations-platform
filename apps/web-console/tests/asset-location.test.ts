import { describe, expect, it } from 'vitest';
import { locationPathText } from '../src/cloud/asset';
import { KIND_TEXT } from '../src/cloud/location-form';

describe('R6-BIZ-R1 F-03 当前位置路径', () => {
  it('按根→叶输出 场地 › 厂区 › 车间，带编号时附编号', () => {
    const path = [
      { id: 's', kind: 'SITE', code: 'S1', name: '一号场地', status: 'ACTIVE' },
      { id: 'p', kind: 'PLANT', code: null, name: '一号厂房', status: 'ACTIVE' },
      { id: 'w', kind: 'WORKSHOP', code: 'W2', name: '二车间', status: 'DISABLED' },
    ] as const;
    expect(locationPathText(path, KIND_TEXT)).toBe('场地·一号场地（S1） › 厂区·一号厂房 › 车间·二车间（W2）');
  });
  it('未知层级类型原样显示，不改名', () => {
    expect(locationPathText([{ id: 'x', kind: 'FUTURE', code: null, name: 'X', status: 'ACTIVE' }] as never, KIND_TEXT)).toBe('FUTURE·X');
  });
  it('空路径为空串（调用方只在 current 非空时使用）', () => {
    expect(locationPathText([], KIND_TEXT)).toBe('');
  });
});
