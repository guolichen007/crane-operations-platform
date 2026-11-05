import { describe, it, expect } from 'vitest';
import { assertPreviewTarget, assertRequiredEnvironment, PREVIEW_ORIGIN } from '../src/cloud/preview-target';

// The Preview UAT target guard must fail closed before the first browser action.
describe('assertPreviewTarget（E2E 目标保护：严格等价）', () => {
  it('合法 Preview 源通过（可带单个结尾斜杠）', () => {
    expect(assertPreviewTarget(PREVIEW_ORIGIN)).toBe(PREVIEW_ORIGIN);
    expect(assertPreviewTarget(PREVIEW_ORIGIN + '/')).toBe(PREVIEW_ORIGIN);
  });
  const rejects: [string, string | undefined][] = [
    ['缺失', undefined],
    ['空字符串', ''],
    ['HTTP', 'http://b4.crane.youdaozd.com'],
    ['正式站', 'https://crane.youdaozd.com'],
    ['显式默认端口', 'https://b4.crane.youdaozd.com:443'],
    ['非默认端口', 'https://b4.crane.youdaozd.com:8443'],
    ['路径', 'https://b4.crane.youdaozd.com/app'],
    ['query', 'https://b4.crane.youdaozd.com/?x=1'],
    ['fragment', 'https://b4.crane.youdaozd.com/#/assets'],
    ['用户名密码', 'https://user:pass@b4.crane.youdaozd.com'],
    ['伪主机后缀', 'https://b4.crane.youdaozd.com.evil.test'],
    ['伪主机前缀', 'https://evil-b4.crane.youdaozd.com'],
    ['结尾点主机', 'https://b4.crane.youdaozd.com.'],
    ['大小写变体', 'https://B4.crane.youdaozd.com'],
    ['多个斜杠', 'https://b4.crane.youdaozd.com//'],
  ];
  for (const [name, value] of rejects) {
    it(`${name} 必须拒绝`, () => {
      expect(() => assertPreviewTarget(value)).toThrow();
    });
  }
});

describe('assertRequiredEnvironment（服务端环境哨兵必须声明）', () => {
  it('b4-preview 通过', () => {
    expect(assertRequiredEnvironment('b4-preview')).toBe('b4-preview');
  });
  for (const value of [undefined, '', 'production', 'B4-PREVIEW', 'b4-preview ']) {
    it(`缺失或错误哨兵 ${JSON.stringify(value)} 必须拒绝`, () => {
      expect(() => assertRequiredEnvironment(value)).toThrow(/B4_REQUIRED_ENVIRONMENT/);
    });
  }
});
