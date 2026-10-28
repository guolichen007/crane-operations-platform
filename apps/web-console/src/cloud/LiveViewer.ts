import type { LiveSession, Shadow } from '../../../../contracts/typescript/types.gen';
import { openLive, renewLive, closeLive, streamUrl } from './api';

export const viewerStorageKey = 'crane.cloud.viewer';
export type ViewerState = 'CONNECTING' | 'LIVE' | 'RECONNECTING' | 'POLLING_FALLBACK' | 'STOPPED';
export interface ViewerSnapshot {
  state: ViewerState;
  session?: LiveSession;
  error: string;
}
interface Options {
  onChange: (value: ViewerSnapshot) => void;
  onShadow: (value: Shadow) => void;
  storage: Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;
  target: EventTarget;
  api?: { openLive: typeof openLive; renewLive: typeof renewLive; closeLive: typeof closeLive };
  socket?: (deviceId: string) => WebSocket;
}

/** Tab-local transport lifecycle. Cloud/Edge lease states and TTLs remain authoritative. */
export class LiveViewer {
  private api;
  private createSocket;
  private session?: LiveSession;
  private socket?: WebSocket;
  private state: ViewerState = 'STOPPED';
  private error = '';
  private intent = false;
  private disposed = false;
  private generation = 0;
  private attempt = 0;
  private timers = new Set<ReturnType<typeof setTimeout>>();
  private initialized?: Promise<void>;
  private attached = false;
  private leave = () => {
    void this.stop(true);
  };

  constructor(private options: Options) {
    this.api = options.api ?? { openLive, renewLive, closeLive };
    this.createSocket = options.socket ?? ((id) => new WebSocket(streamUrl(id)));
  }
  private publish() {
    if (!this.disposed)
      this.options.onChange({ state: this.state, session: this.session, error: this.error });
  }
  private stored() {
    try {
      return this.options.storage.getItem(viewerStorageKey);
    } catch {
      return null;
    }
  }
  private forget(id: string) {
    try {
      if (this.stored() === id) this.options.storage.removeItem(viewerStorageKey);
    } catch {
      /* TTL fallback */
    }
  }
  initialize(): Promise<void> {
    if (!this.attached && !this.disposed) {
      this.options.target.addEventListener('pagehide', this.leave);
      this.options.target.addEventListener('beforeunload', this.leave);
      this.options.target.addEventListener('cloud-session-expired', this.leave);
      this.attached = true;
    }
    // Never infer LIVE intent from sessionStorage after reload.
    return (this.initialized ??= (async () => {
      const stale = this.stored();
      if (stale) {
        // Reload is an ordinary bounded request; keepalive is only for leaving.
        await this.api.closeLive(stale, false);
        this.forget(stale);
      }
    })().catch((e) => {
      this.initialized = undefined;
      this.error = `旧租约清理失败；可重试，TTL 仍会兜底。${(e as Error).message}`;
      this.publish();
      throw e;
    }));
  }
  private later(fn: () => void, ms: number) {
    const timer = setTimeout(() => {
      this.timers.delete(timer);
      fn();
    }, ms);
    this.timers.add(timer);
  }
  private resetTransport() {
    for (const timer of this.timers) clearTimeout(timer);
    this.timers.clear();
    if (this.socket) {
      this.socket.onopen = this.socket.onclose = this.socket.onerror = this.socket.onmessage = null;
      this.socket.close();
      this.socket = undefined;
    }
  }
  private valid(generation: number) {
    return (
      !this.disposed &&
      this.intent &&
      generation === this.generation &&
      !!this.session &&
      Date.parse(this.session.expiresAt) > Date.now()
    );
  }
  async start(deviceId: string) {
    const generation = ++this.generation;
    this.intent = true;
    const previous = this.session?.sessionId;
    this.session = undefined;
    this.resetTransport();
    this.state = 'CONNECTING';
    this.error = '';
    this.publish();
    try {
      await this.initialize();
      if (previous) {
        this.forget(previous);
        await this.api.closeLive(previous);
      }
      if (this.disposed || generation !== this.generation || !this.intent) return;
      const session = await this.api.openLive(deviceId);
      // Stop/navigation may happen while POST is in flight. Close that late lease.
      if (this.disposed || generation !== this.generation || !this.intent) {
        await this.api.closeLive(session.sessionId, true).catch(() => {});
        return;
      }
      this.session = session;
      try {
        this.options.storage.setItem(viewerStorageKey, session.sessionId);
      } catch {
        /* TTL fallback */
      }
      if (!this.valid(generation)) throw Error('云端返回的 Viewer Lease 已到期');
      this.attempt = 0;
      this.watchExpiry(generation);
      this.scheduleRenew(generation);
      this.connect(deviceId, generation);
      this.publish();
    } catch (e) {
      if (this.disposed || generation !== this.generation) return;
      await this.fail((e as Error).message);
    }
  }
  private watchExpiry(generation: number) {
    const expires = Date.parse(this.session!.expiresAt);
    this.later(
      () => {
        if (generation !== this.generation || !this.intent) return;
        if (this.session && Date.parse(this.session.expiresAt) > Date.now()) {
          this.watchExpiry(generation);
          return;
        }
        void this.fail('Viewer Lease 已到期，已停止实时查看');
      },
      Math.max(1, expires - Date.now() + 1)
    );
  }
  private scheduleRenew(generation: number) {
    this.later(
      () => {
        void this.renew(generation);
      },
      Math.min(60000, Math.max(1000, (this.session!.ttlSeconds * 1000) / 3))
    );
  }
  private async renew(generation: number) {
    if (!this.valid(generation)) return;
    try {
      const session = await this.api.renewLive(this.session!.sessionId);
      if (!this.valid(generation)) return;
      this.session = session;
      if (!this.valid(generation)) {
        await this.fail('Viewer Lease 已到期');
        return;
      }
      this.scheduleRenew(generation);
      this.publish();
    } catch {
      if (generation === this.generation && this.intent)
        await this.fail('续租失败，已停止实时查看；TTL 仍会兜底');
    }
  }
  private connect(deviceId: string, generation: number) {
    if (!this.valid(generation)) return;
    try {
      const socket = this.createSocket(deviceId);
      this.socket = socket;
      socket.onopen = () => {
        if (!this.valid(generation) || this.socket !== socket) return;
        this.state = 'LIVE';
        this.error = '';
        this.attempt = 0;
        this.publish();
      };
      socket.onmessage = (event) => {
        if (!this.valid(generation) || this.socket !== socket) return;
        try {
          const shadow = JSON.parse(event.data) as Shadow;
          if (shadow.deviceId === deviceId) this.options.onShadow(shadow);
        } catch {
          /* A malformed frame cannot alter the current Shadow. */
        }
      };
      socket.onerror = () => {
        /* Native WebSocket emits close after a transport error. */
      };
      socket.onclose = () => {
        if (this.socket !== socket) return;
        this.socket = undefined;
        this.reconnect(deviceId, generation);
      };
    } catch {
      this.reconnect(deviceId, generation);
    }
  }
  private reconnect(deviceId: string, generation: number) {
    if (!this.valid(generation)) return;
    this.state = 'RECONNECTING';
    this.error = '实时流中断 · 正在重连';
    this.publish();
    const delay = [1000, 2000, 5000, 10000][Math.min(this.attempt++, 3)]!;
    this.later(() => this.connect(deviceId, generation), delay);
  }
  pollingAvailable() {
    if (
      this.valid(this.generation) &&
      (this.state === 'RECONNECTING' || this.state === 'POLLING_FALLBACK')
    ) {
      this.state = 'POLLING_FALLBACK';
      this.error = '最近状态可用 · 实时流正在恢复';
      this.publish();
    }
  }
  private async fail(message: string) {
    const stopped = this.stop();
    this.error = message;
    this.publish();
    await stopped;
  }
  async stop(unload = false) {
    ++this.generation;
    this.intent = false;
    this.resetTransport();
    const id = this.session?.sessionId;
    this.session = undefined;
    this.state = 'STOPPED';
    this.error = '';
    this.publish();
    if (!id) return;
    // On unload keep the id until a successful DELETE so reload can retry cleanup.
    if (!unload) this.forget(id);
    try {
      await this.api.closeLive(id, unload);
      this.forget(id);
    } catch {
      /* Server TTL + grace remains final fallback. */
    }
  }
  dispose() {
    this.disposed = true;
    this.options.target.removeEventListener('pagehide', this.leave);
    this.options.target.removeEventListener('beforeunload', this.leave);
    this.options.target.removeEventListener('cloud-session-expired', this.leave);
    return this.stop(true);
  }
}
