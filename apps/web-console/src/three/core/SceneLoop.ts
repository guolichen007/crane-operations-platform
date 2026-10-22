export class SceneLoop {
  private id = 0;
  private previous: number | undefined;
  private elapsed = 0;
  constructor(private update: (dt: number, t: number) => void) {
    document.addEventListener('visibilitychange', this.resetFrame);
    this.id = requestAnimationFrame(this.tick);
  }
  private resetFrame = () => {
    this.previous = undefined;
  };
  private tick = (ms: number) => {
    // Camera transitions and the tour must share real visible time at low FPS.
    // Capping delta slowed the camera while the tour kept advancing in real time.
    const dt = this.previous === undefined ? 0 : Math.max(0, (ms - this.previous) / 1000);
    this.previous = document.hidden ? undefined : ms;
    if (!document.hidden) {
      this.elapsed += dt;
      this.update(dt, this.elapsed);
    }
    this.id = requestAnimationFrame(this.tick);
  };
  dispose() {
    document.removeEventListener('visibilitychange', this.resetFrame);
    cancelAnimationFrame(this.id);
  }
}
