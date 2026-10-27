import type { QueryClient } from "@tanstack/react-query";
import { callRecorded, hookChanged, resync } from "@/api/cache";
import { paths } from "@/api/client";
import { hookEvent, problem, summary } from "@/api/schemas";
import { backoffDelay } from "./backoff";

/**
 * LiveStatus: crowded is reconnecting because the viewer's device has too
 * many streams open (429); refused is no more retries, because the relay
 * won't stream to the viewer (403), until the tab is shown again or the
 * stream it wants changes.
 */
export type LiveStatus = "off" | "connecting" | "live" | "reconnecting" | "crowded" | "refused";

/** LiveSnapshot is the stream's status, with the relay's reason when it refused. */
export interface LiveSnapshot {
  status: LiveStatus;
  detail?: string;
}

export interface LiveStreamOptions {
  EventSource?: typeof EventSource;
  fetch?: typeof fetch;
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
 * server closing a lagging stream) reconnect with backoff. EventSource
 * doesn't say why a connect failed, so a failed one is asked again with
 * fetch, to tell a refusal (which stops) from too many streams. start and stop
 * bracket its life, so React's StrictMode can stop and start it again.
 */
export class LiveStream {
  private queryClient: QueryClient;
  private EventSource: typeof EventSource;
  private fetch: typeof fetch;
  private document: NonNullable<LiveStreamOptions["document"]>;
  private base: number;
  private max: number;
  private user = false;
  private allHolders = 0;
  private source: EventSource | null = null;
  private failed: EventSource | null = null; // the last stream that failed, which a status check answers for
  private url: string | null = null;
  private attempt = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private started = false;
  private needsRefetch = false; // a stream was closed or failed, so events may have been missed
  private state: LiveSnapshot = { status: "off" };
  private listeners = new Set<() => void>();
  private onVisibility = () => this.sync();

  constructor(queryClient: QueryClient, options: LiveStreamOptions = {}) {
    this.queryClient = queryClient;
    this.EventSource = options.EventSource ?? EventSource;
    this.fetch = options.fetch ?? ((input, init) => fetch(input, init));
    this.document = options.document ?? document;
    this.base = options.base ?? 1000;
    this.max = options.max ?? 30_000;
  }

  start(): void {
    if (this.started) return;
    this.started = true;
    this.document.addEventListener("visibilitychange", this.onVisibility);
    this.sync();
  }

  stop(): void {
    this.started = false;
    this.document.removeEventListener("visibilitychange", this.onVisibility);
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
    return this.state.status;
  }

  snapshot(): LiveSnapshot {
    return this.state;
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  private wanted(): string | null {
    if (!this.started || this.document.hidden) return null;
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
    const source = new this.EventSource(this.url!);
    this.source = source;
    if (this.state.status !== "reconnecting" && this.state.status !== "crowded")
      this.setState("connecting");
    source.onopen = () => {
      if (this.needsRefetch) resync(this.queryClient);
      this.needsRefetch = false;
      this.attempt = 0;
      this.setState("live");
    };
    source.onerror = () => {
      source.close();
      this.source = null;
      this.failed = source;
      this.needsRefetch = true;
      if (this.state.status !== "crowded") this.setState("reconnecting");
      void this.check(this.url!, source);
      this.timer = setTimeout(
        () => this.connect(),
        backoffDelay(++this.attempt, { base: this.base, max: this.max }),
      );
    };
    source.addEventListener("call", (event) => {
      const result = summary.safeParse(parse((event as MessageEvent).data));
      if (result.success) callRecorded(this.queryClient, result.data);
    });
    source.addEventListener("hook", (event) => {
      const result = hookEvent.safeParse(parse((event as MessageEvent).data));
      if (result.success) hookChanged(this.queryClient, result.data.id);
    });
  }

  private close(): void {
    this.source?.close();
    this.source = null;
    this.failed = null; // a status check still out answers for nothing now
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    this.url = null;
  }

  /**
   * check asks for url again to learn why the stream failed, and lets go of
   * the answer at once. It counts only while failed is the last stream to
   * fail and no other has started since.
   */
  private async check(url: string, failed: EventSource): Promise<void> {
    const controller = new AbortController();
    let response: Response;
    try {
      response = await this.fetch(url, {
        headers: { Accept: "text/event-stream" },
        signal: controller.signal,
      });
    } catch {
      return;
    }
    const detail = response.ok
      ? undefined
      : await response.json().then(
          (body: unknown) => problem.safeParse(body).data?.detail,
          () => undefined,
        );
    controller.abort();
    if (this.source || this.failed !== failed) return;
    if (response.status === 403) {
      if (this.timer) clearTimeout(this.timer);
      this.timer = null;
      this.setState("refused", detail ?? "the relay refused the event stream");
      resync(this.queryClient); // the entry document too: the viewer may have lost their role
    } else {
      this.setState(response.status === 429 ? "crowded" : "reconnecting");
    }
  }

  private setState(status: LiveStatus, detail?: string): void {
    if (status === this.state.status && detail === this.state.detail) return;
    this.state = detail === undefined ? { status } : { status, detail };
    for (const listener of this.listeners) listener();
  }
}

function parse(data: unknown): unknown {
  try {
    return JSON.parse(String(data));
  } catch {
    return undefined;
  }
}
