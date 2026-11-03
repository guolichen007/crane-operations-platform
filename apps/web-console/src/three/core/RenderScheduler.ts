export type RenderMode = 'INTERACTING' | 'CAMERA' | 'TOUR' | 'LIVE' | 'IDLE' | 'HIDDEN';

const FPS_BY_MODE: Record<RenderMode, number> = {
  INTERACTING: 60,
  CAMERA: 60,
  TOUR: 60,
  LIVE: 30,
  IDLE: 0, // invalidate-only: render once per invalidation, then go quiet.
  HIDDEN: 0,
};

/**
 * RenderScheduler is the single authority for how often the scene renders.
 * IDLE renders only when invalidated (no steady-state GPU work); HIDDEN renders
 * nothing. Modes other than IDLE/HIDDEN render at their budgeted FPS.
 */
export class RenderScheduler {
  private mode: RenderMode = 'LIVE';
  private dirty = true;
  private last = -Infinity;
  onModeChange?: (mode: RenderMode) => void;

  get current(): RenderMode {
    return this.mode;
  }

  setMode(mode: RenderMode) {
    if (mode === this.mode) return;
    this.mode = mode;
    this.dirty = true;
    this.onModeChange?.(mode);
  }

  /** Marks the scene dirty so the next scheduled frame actually renders. */
  invalidate() {
    this.dirty = true;
  }

  /** Returns true when the frame at `now` (rAF ms) should render. */
  shouldRender(now: number): boolean {
    if (this.mode === 'HIDDEN') return false;
    if (this.mode === 'IDLE') {
      if (!this.dirty) return false;
      this.dirty = false;
      return true;
    }
    const fps = FPS_BY_MODE[this.mode];
    if (fps <= 0) return false;
    const interval = 1000 / fps;
    if (now - this.last < interval - 1) return false;
    this.last = now;
    return true;
  }
}
