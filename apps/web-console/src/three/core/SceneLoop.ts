export class SceneLoop {
  private id = 0;
  private previous = 0;
  private elapsed = 0;
  constructor(private update: (dt: number, t: number) => void) {
    this.id = requestAnimationFrame(this.tick);
  }
  private tick = (ms: number) => {
    const dt = Math.min((ms - this.previous) / 1000, 0.05);
    this.previous = ms;
    if (!document.hidden) {
      this.elapsed += dt;
      this.update(dt, this.elapsed);
    }
    this.id = requestAnimationFrame(this.tick);
  };
  dispose() {
    cancelAnimationFrame(this.id);
  }
}
