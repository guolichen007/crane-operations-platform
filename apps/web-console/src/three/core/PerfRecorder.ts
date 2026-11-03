export type PerfSample = {
  fps: number;
  frameMs: number;
  frameP95: number;
  frameP99: number;
  fps1PercentLow: number;
  longestBlockingMs: number;
  forcedReflowMaxMs: number;
  samples: number;
};

const MAX_SAMPLES = 1200;

/**
 * PerfRecorder samples frame pacing in the page for a bounded window and
 * computes FPS median / 1% low / frame P95/P99 plus the largest Long Animation
 * Frame and forced reflow, when the browser exposes them (LoAF is Chromium-only
 * and gated behind performance.performanceLongAnimationFrameTiming).
 */
export class PerfRecorder {
  private frameMs: number[] = [];
  private last = 0;
  private loafMax = 0;
  private reflowMax = 0;
  private running = false;
  private rafId = 0;
  private observer?: PerformanceObserver;

  start() {
    if (this.running) return;
    this.running = true;
    this.frameMs = [];
    this.last = performance.now();
    this.loafMax = 0;
    this.reflowMax = 0;
    const loaf: { entryType: string; duration: number }[] = (
      performance as unknown as {
        getEntriesByType: (t: string) => { entryType: string; duration: number }[];
      }
    ).getEntriesByType?.('long-animation-frame') ?? [];
    this.loafMax = loaf.reduce((m, e) => Math.max(m, e.duration), 0);
    try {
      const LoAF = (
        PerformanceObserver as unknown as { supportedEntryTypes?: string[] }
      ).supportedEntryTypes;
      if (LoAF?.includes('long-animation-frame')) {
        this.observer = new PerformanceObserver((list) => {
          for (const e of list.getEntries())
            this.loafMax = Math.max(this.loafMax, e.duration);
        });
        this.observer.observe({ type: 'long-animation-frame', buffered: true });
      }
    } catch {
      this.observer = undefined;
    }
    const measureForcedReflow = () => {
      // Browsers do not expose forced reflow directly; the closest observable is
      // a layout-triggering read. We time one `offsetHeight` read as a proxy and
      // never store its result.
      const t0 = performance.now();
      void document.documentElement.offsetHeight;
      this.reflowMax = Math.max(this.reflowMax, performance.now() - t0);
    };
    const tick = (now: number) => {
      if (!this.running) return;
      const ms = now - this.last;
      if (ms > 0) {
        this.frameMs.push(ms);
        if (this.frameMs.length > MAX_SAMPLES) this.frameMs.shift();
      }
      this.last = now;
      measureForcedReflow();
      this.rafId = requestAnimationFrame(tick);
    };
    this.rafId = requestAnimationFrame(tick);
  }

  stop(): PerfSample {
    this.running = false;
    cancelAnimationFrame(this.rafId);
    this.observer?.disconnect();
    this.observer = undefined;
    const values = [...this.frameMs].sort((a, b) => a - b);
    const samples = values.length;
    if (!samples)
      return {
        fps: 0, frameMs: 0, frameP95: 0, frameP99: 0,
        fps1PercentLow: 0, longestBlockingMs: this.loafMax,
        forcedReflowMaxMs: this.reflowMax, samples: 0,
      };
    const pct = (p: number) => values[Math.min(samples - 1, Math.floor((p / 100) * samples))]!;
    const fps = values.map((v) => 1000 / v).sort((a, b) => a - b);
    const median = (arr: number[]) =>
      arr.length % 2 ? arr[(arr.length - 1) / 2]! : (arr[arr.length / 2 - 1]! + arr[arr.length / 2]!) / 2;
    const p1 = (arr: number[]) => arr[Math.max(0, Math.floor(0.01 * arr.length))]!;
    return {
      fps: Math.round(median(fps) * 10) / 10,
      frameMs: Math.round(median(values) * 100) / 100,
      frameP95: Math.round(pct(95) * 100) / 100,
      frameP99: Math.round(pct(99) * 100) / 100,
      fps1PercentLow: Math.round(p1(fps) * 10) / 10,
      longestBlockingMs: Math.round(this.loafMax * 100) / 100,
      forcedReflowMaxMs: Math.round(this.reflowMax * 100) / 100,
      samples,
    };
  }
}
