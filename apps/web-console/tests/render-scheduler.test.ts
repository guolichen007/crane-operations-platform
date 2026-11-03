import { describe, expect, it } from 'vitest';
import { RenderScheduler } from '../src/three/core/RenderScheduler';

describe('RenderScheduler 帧调度契约', () => {
  it('IDLE 只在 invalidate 后渲染一次，之后静默', () => {
    const s = new RenderScheduler();
    s.setMode('IDLE');
    expect(s.shouldRender(0)).toBe(true);
    expect(s.shouldRender(16)).toBe(false);
    expect(s.shouldRender(32)).toBe(false);
    s.invalidate();
    expect(s.shouldRender(48)).toBe(true);
    expect(s.shouldRender(64)).toBe(false);
  });
  it('HIDDEN 永不渲染', () => {
    const s = new RenderScheduler();
    s.setMode('HIDDEN');
    expect(s.shouldRender(0)).toBe(false);
    s.invalidate();
    expect(s.shouldRender(16)).toBe(false);
  });
  it('LIVE 按 30FPS 节流', () => {
    const s = new RenderScheduler();
    s.setMode('LIVE');
    expect(s.shouldRender(0)).toBe(true);
    // 30FPS -> ~33ms interval; a 16ms tick must be skipped.
    expect(s.shouldRender(16)).toBe(false);
    expect(s.shouldRender(33)).toBe(true);
  });
  it('INTERACTING 按 60FPS 节流', () => {
    const s = new RenderScheduler();
    s.setMode('INTERACTING');
    expect(s.shouldRender(0)).toBe(true);
    expect(s.shouldRender(8)).toBe(false);
    expect(s.shouldRender(17)).toBe(true);
  });
  it('切换模式会立即标记脏帧', () => {
    const s = new RenderScheduler();
    s.setMode('IDLE');
    expect(s.shouldRender(0)).toBe(true);
    expect(s.shouldRender(16)).toBe(false);
    s.setMode('LIVE');
    expect(s.shouldRender(32)).toBe(true);
  });
});
