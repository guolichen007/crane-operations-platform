import { afterEach, describe, expect, it, vi } from 'vitest';
import { SceneLoop } from '../src/three/core/SceneLoop';

afterEach(() => vi.unstubAllGlobals());
function harness() {
  const document = Object.assign(new EventTarget(), { hidden: false });
  let frame: FrameRequestCallback;
  const cancel = vi.fn();
  vi.stubGlobal('document', document);
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
    frame = callback;
    return 1;
  });
  vi.stubGlobal('cancelAnimationFrame', cancel);
  const samples: Array<{ dt: number; elapsed: number }> = [];
  const loop = new SceneLoop((dt, elapsed) => samples.push({ dt, elapsed }));
  return { document, loop, samples, cancel, step: (ms: number) => frame(ms) };
}
describe('可见时间与低帧率恢复', () => {
  it('2 FPS 时按真实可见时间推进，镜头不会比巡演时钟慢十倍', () => {
    const h = harness();
    h.step(1000);
    h.step(1500);
    h.step(2000);
    expect(h.samples).toEqual([
      { dt: 0, elapsed: 0 },
      { dt: 0.5, elapsed: 0.5 },
      { dt: 0.5, elapsed: 1 },
    ]);
    h.loop.dispose();
    expect(h.cancel).toHaveBeenCalledWith(1);
  });
  it('后台标签恢复后不把隐藏时间补进镜头与动画', () => {
    const h = harness();
    h.step(1000);
    h.step(1500);
    h.document.hidden = true;
    h.document.dispatchEvent(new Event('visibilitychange'));
    h.step(10000);
    expect(h.samples).toHaveLength(2);
    h.document.hidden = false;
    h.document.dispatchEvent(new Event('visibilitychange'));
    h.step(11000);
    h.step(11500);
    expect(h.samples.at(-2)).toEqual({ dt: 0, elapsed: 0.5 });
    expect(h.samples.at(-1)).toEqual({ dt: 0.5, elapsed: 1 });
    h.loop.dispose();
  });
});
