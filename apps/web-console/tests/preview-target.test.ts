import { describe, it, expect } from 'vitest';
import { assertPreviewTarget, PREVIEW_HOST } from '../src/cloud/preview-target';

// The Preview UAT target guard must fail closed before the first browser action:
// missing target, HTTP, and the production host are all rejected.
describe('assertPreviewTarget（E2E 目标保护）', () => {
  it('缺失目标必须拒绝', () => {
    expect(() => assertPreviewTarget(undefined)).toThrow(/A1_WEB_ORIGIN is required/);
  });
  it('空字符串目标必须拒绝', () => {
    expect(() => assertPreviewTarget('')).toThrow(/A1_WEB_ORIGIN is required/);
  });
  it('HTTP 必须拒绝', () => {
    expect(() => assertPreviewTarget('http://' + PREVIEW_HOST)).toThrow(/HTTPS/);
  });
  it('正式站必须拒绝（首次浏览器动作前）', () => {
    expect(() => assertPreviewTarget('https://crane.youdaozd.com')).toThrow(/refuses host|production/);
  });
  it('合法 Preview 域名通过', () => {
    expect(assertPreviewTarget('https://' + PREVIEW_HOST)).toBe('https://' + PREVIEW_HOST);
  });
});
