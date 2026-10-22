export type TourStep = { at: number; title: string; action: () => void | Promise<void> };
export class DemoTourController {
  private timer: ReturnType<typeof setInterval> | undefined;
  private start = 0;
  private elapsed = 0;
  private index = -1;
  private generation = 0;
  running = false;
  paused = false;
  constructor(
    private steps: TourStep[],
    private onChange: (running: boolean, paused: boolean, title: string, progress: number) => void
  ) {}
  play() {
    if (this.running && !this.paused) return;
    this.running = true;
    this.paused = false;
    this.start = performance.now() - this.elapsed;
    this.timer = setInterval(() => void this.tick(), 100);
    void this.tick();
  }
  private async tick() {
    if (!this.running || this.paused) return;
    const generation = this.generation;
    this.elapsed = performance.now() - this.start;
    const time = this.elapsed / 1000;
    if (time >= 45) {
      this.elapsed = 0;
      this.start = performance.now();
      this.index = -1;
      return;
    }
    const index = this.steps.reduce((found, s, i) => (s.at <= time ? i : found), -1);
    if (index !== this.index) {
      this.index = index;
      await this.steps[index]?.action();
      if (generation !== this.generation) return;
    }
    this.onChange(
      this.running,
      this.paused,
      this.steps[this.index]?.title ?? '全国设备态势',
      time / 45
    );
  }
  pause() {
    if (!this.running) return;
    this.paused = true;
    clearInterval(this.timer);
    this.onChange(true, true, this.steps[this.index]?.title ?? '', this.elapsed / 45000);
  }
  exit() {
    this.generation++;
    clearInterval(this.timer);
    this.running = false;
    this.paused = false;
    this.elapsed = 0;
    this.index = -1;
    this.onChange(false, false, '', 0);
  }
  dispose() {
    this.exit();
  }
}
