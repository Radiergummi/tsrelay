import type { QueryClient } from "@tanstack/react-query";
import { callRecorded, hookChanged, resync } from "@/api/cache";
import { paths } from "@/api/client";
import { hookEvent, summary } from "@/api/schemas";
import { backoffDelay } from "./backoff";

export type LiveStatus = "off" | "connecting" | "live" | "reconnecting";

export interface LiveStreamOptions {
  EventSource?: typeof EventSource;
  document?: Pick<Document, "hidden" | "addEventListener" | "removeEventListener">;
  base?: number;
  max?: number;
}

/**
 * LiveStream keeps one EventSource per tab on /hooks, the hook list's
 * event stream (DESIGN.md §10.1): for the viewer's own hooks, or ?all=true
 * while a page (admin, or another user's hook) holds acquireAll. One
 * stream serves every page, so a tab never holds more than one connection. It closes while the tab is
 * hidden, and after a reopen or a reconnect it refetches the active
 * queries, since events may have been missed. Errors (including the
 * server closing a lagging stream) reconnect with backoff. start and stop
 * bracket its life, so React's StrictMode can stop and start it again.
 */
export class LiveStream {
  private qc: QueryClient;
  private ES: typeof EventSource;
  private doc: NonNullable<LiveStreamOptions["document"]>;
  private base: number;
  private max: number;
  private user = false;
  private allHolders = 0;
  private source: EventSource | null = null;
  private url: string | null = null;
  private attempt = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private started = false;
  private needsRefetch = false; // a stream was closed or failed, so events may have been missed
  private state: LiveStatus = "off";
  private listeners = new Set<() => void>();
  private onVisibility = () => this.sync();

  constructor(qc: QueryClient, opts: LiveStreamOptions = {}) {
    this.qc = qc;
    this.ES = opts.EventSource ?? EventSource;
    this.doc = opts.document ?? document;
    this.base = opts.base ?? 1000;
    this.max = opts.max ?? 30_000;
  }

  start(): void {
    if (this.started) return;
    this.started = true;
    this.doc.addEventListener("visibilitychange", this.onVisibility);
    this.sync();
  }

  stop(): void {
    this.started = false;
    this.doc.removeEventListener("visibilitychange", this.onVisibility);
    this.close();
    this.setState("off");
  }

  setUser(user: boolean): void {
    this.user = user;
    this.sync();
  }

  acquireAll(): () => void {
    this.allHolders++;
    this.sync();
    let released = false;
    return () => {
      if (released) return;
      released = true;
      this.allHolders--;
      this.sync();
    };
  }

  status(): LiveStatus {
    return this.state;
  }

  subscribe(fn: () => void): () => void {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  private wanted(): string | null {
    if (!this.started || this.doc.hidden) return null;
    if (this.allHolders > 0) return paths.hooks(true);
    return this.user ? paths.hooks(false) : null;
  }

  private sync(): void {
    const want = this.wanted();
    if (want === this.url && (this.source || this.timer)) return;
    if (this.source || this.timer) this.needsRefetch = true;
    this.close();
    if (!want) {
      this.setState("off");
      return;
    }
    this.url = want;
    this.attempt = 0;
    this.connect();
  }

  private connect(): void {
    this.timer = null;
    const es = new this.ES(this.url!);
    this.source = es;
    if (this.state !== "reconnecting") this.setState("connecting");
    es.onopen = () => {
      if (this.needsRefetch) resync(this.qc);
      this.needsRefetch = false;
      this.attempt = 0;
      this.setState("live");
    };
    es.onerror = () => {
      es.close();
      this.source = null;
      this.needsRefetch = true;
      this.setState("reconnecting");
      this.timer = setTimeout(
        () => this.connect(),
        backoffDelay(++this.attempt, { base: this.base, max: this.max }),
      );
    };
    es.addEventListener("call", (e) => {
      const r = summary.safeParse(parse((e as MessageEvent).data));
      if (r.success) callRecorded(this.qc, r.data);
    });
    es.addEventListener("hook", (e) => {
      const r = hookEvent.safeParse(parse((e as MessageEvent).data));
      if (r.success) hookChanged(this.qc, r.data.id);
    });
  }

  private close(): void {
    this.source?.close();
    this.source = null;
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    this.url = null;
  }

  private setState(s: LiveStatus): void {
    if (s === this.state) return;
    this.state = s;
    for (const fn of this.listeners) fn();
  }
}

function parse(data: unknown): unknown {
  try {
    return JSON.parse(String(data));
  } catch {
    return undefined;
  }
}
