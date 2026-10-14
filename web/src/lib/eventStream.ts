import type { QueryClient } from "@tanstack/react-query";
import { paths } from "@/api/client";
import { keys } from "@/api/queries";
import { hookEvent, summary, type Hook, type Me, type Summary } from "@/api/schemas";
import { backoffDelay } from "./backoff";

export type LiveStatus = "off" | "connecting" | "live" | "reconnecting";

export interface LiveStreamOptions {
  EventSource?: typeof EventSource;
  document?: Pick<Document, "hidden" | "addEventListener" | "removeEventListener">;
  base?: number;
  max?: number;
}

const newer = (s: Summary, last: Summary | undefined) =>
  !last || last.id === s.id || Date.parse(s.receivedAt) >= Date.parse(last.receivedAt);

/**
 * applyCallEvent folds a call summary into the cached lists, the hook's
 * lastCall, and refreshes the call if it's open. The list keeps no more
 * calls than the relay does (/me's policy.callsPerHook). A list being
 * fetched is refetched instead, since the answer in flight may predate the
 * event and would overwrite it.
 */
export function applyCallEvent(qc: QueryClient, s: Summary): void {
  const key = keys.calls(s.hookId);
  if (qc.getQueryState(key)?.fetchStatus === "fetching") {
    void qc.invalidateQueries({ queryKey: key });
  } else {
    const cap = qc.getQueryData<Me>(keys.me)?.policy.callsPerHook;
    qc.setQueryData<Summary[]>(key, (list) => {
      if (!list) return list;
      const rest = list.filter((c) => c.id !== s.id);
      const sorted = [s, ...rest].sort(
        (a, b) => Date.parse(b.receivedAt) - Date.parse(a.receivedAt),
      );
      return cap === undefined ? sorted : sorted.slice(0, cap);
    });
  }
  const withLast = (h: Hook): Hook =>
    h.id === s.hookId && newer(s, h.lastCall) ? { ...h, lastCall: s } : h;
  qc.setQueriesData<Hook[]>({ queryKey: ["hooks"] }, (list) => list?.map(withLast));
  qc.setQueryData<Hook>(keys.hook(s.hookId), (h) => (h ? withLast(h) : h));
  void qc.invalidateQueries({ queryKey: keys.call(s.hookId, s.id) });
}

/** applyHookEvent refetches a hook that changed (or was deleted), the lists and /me's hook count. */
export function applyHookEvent(qc: QueryClient, id: string): void {
  void qc.invalidateQueries({ queryKey: keys.hook(id) });
  void qc.invalidateQueries({ queryKey: ["hooks"] });
  void qc.invalidateQueries({ queryKey: keys.me });
}

/**
 * LiveStream keeps one EventSource per tab on /api/v1/events (DESIGN.md
 * §10.1): for the viewer's own hooks, or ?all=true while a page (admin, or
 * another user's hook) holds acquireAll. It closes while the tab is
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
    if (this.allHolders > 0) return paths.events(true);
    return this.user ? paths.events(false) : null;
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
      if (this.needsRefetch) void this.qc.invalidateQueries({ type: "active" });
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
      if (r.success) applyCallEvent(this.qc, r.data);
    });
    es.addEventListener("hook", (e) => {
      const r = hookEvent.safeParse(parse((e as MessageEvent).data));
      if (r.success) applyHookEvent(this.qc, r.data.id);
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
