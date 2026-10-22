export class ResourceManager<T> {
  private values = new Map<string, Promise<T>>();
  constructor(private limit = 12) {}
  get(key: string, load: () => Promise<T>): Promise<T> {
    const found = this.values.get(key);
    if (found) return found;
    const value = load().catch((e) => {
      this.values.delete(key);
      throw e;
    });
    this.values.set(key, value);
    if (this.values.size > this.limit) this.values.delete(this.values.keys().next().value!);
    return value;
  }
  clear() {
    this.values.clear();
  }
}
