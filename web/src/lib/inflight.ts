// Share concurrent reads only. Settled values are never cached. Callers scope
// keys by authentication identity and invalidate on writes.
export class InflightReads {
  private pending = new Map<string, Promise<unknown>>();

  run<T>(key: string, load: () => Promise<T>): Promise<T> {
    const existing = this.pending.get(key);
    if (existing) return existing as Promise<T>;
    const request = load().finally(() => {
      if (this.pending.get(key) === request) this.pending.delete(key);
    });
    this.pending.set(key, request);
    return request;
  }

  clear(): void {
    this.pending.clear();
  }
}
