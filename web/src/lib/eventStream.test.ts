import { QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { keys } from "@/api/cache";
import type { Hook, Summary } from "@/api/schemas";
import { fixtures } from "@/test/fixtures";
import { LiveStream } from "./eventStream";

class FakeSource {
  static all: FakeSource[] = [];
  listeners: Record<string, ((e: MessageEvent) => void)[]> = {};
  closed = false;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  url: string;
  constructor(url: string) {
    this.url = url;
    FakeSource.all.push(this);
  }
  addEventListener(type: string, fn: (e: MessageEvent) => void) {
    (this.listeners[type] ??= []).push(fn);
  }
  close() {
    this.closed = true;
  }
  emit(type: string, data: unknown) {
    for (const fn of this.listeners[type] ?? [])
      fn(new MessageEvent(type, { data: JSON.stringify(data) }));
  }
}
const open = () => FakeSource.all.filter((s) => !s.closed);
const latest = () => FakeSource.all.at(-1)!;

let doc: {
  hidden: boolean;
  listeners: (() => void)[];
  addEventListener: (t: string, f: () => void) => void;
  removeEventListener: () => void;
};
let qc: QueryClient;
let stream: LiveStream;
// probe answers the status check after a failed connect; a 200 by default.
let probe: ReturnType<typeof vi.fn<typeof fetch>>;

beforeEach(() => {
  vi.useFakeTimers();
  FakeSource.all = [];
  doc = {
    hidden: false,
    listeners: [],
    addEventListener: (_, f) => doc.listeners.push(f),
    removeEventListener: () => {},
  };
  qc = new QueryClient();
  probe = vi.fn<typeof fetch>(async () => new Response(null, { status: 200 }));
  stream = new LiveStream(qc, {
    EventSource: FakeSource as never,
    fetch: probe,
    document: doc as never,
    base: 1000,
    max: 30_000,
  });
  stream.start();
});
afterEach(() => {
  stream.stop();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

test("users get one stream, switched to ?all=true while a page asks for it", () => {
  stream.setUser(false);
  expect(open()).toHaveLength(0);
  const release = stream.acquireAll();
  expect(open().map((s) => s.url)).toEqual(["/hooks?all=true"]);
  release();
  expect(open()).toHaveLength(0);
  stream.setUser(true);
  expect(open().map((s) => s.url)).toEqual(["/hooks"]);
  const r2 = stream.acquireAll();
  expect(open().map((s) => s.url)).toEqual(["/hooks?all=true"]);
  r2();
  expect(open().map((s) => s.url)).toEqual(["/hooks"]);
});

test("closed while hidden; reopening invalidates the active queries", () => {
  stream.setUser(true);
  latest().onopen?.();
  const spy = vi.spyOn(qc, "invalidateQueries");
  doc.hidden = true;
  doc.listeners.forEach((f) => f());
  expect(open()).toHaveLength(0);
  doc.hidden = false;
  doc.listeners.forEach((f) => f());
  expect(open()).toHaveLength(1);
  latest().onopen?.();
  expect(spy).toHaveBeenCalledWith({ type: "active" });
});

test("reconnects with backoff after errors, and refetches once back", () => {
  vi.spyOn(Math, "random").mockReturnValue(0.5); // no jitter: 1 s, then 2 s
  stream.setUser(true);
  latest().onopen?.();
  expect(stream.status()).toBe("live");
  latest().onerror?.();
  expect(stream.status()).toBe("reconnecting");
  expect(open()).toHaveLength(0);
  vi.advanceTimersByTime(1000);
  expect(open()).toHaveLength(1);
  latest().onerror?.();
  vi.advanceTimersByTime(1999);
  expect(open()).toHaveLength(0);
  vi.advanceTimersByTime(1);
  expect(open()).toHaveLength(1);
  const spy = vi.spyOn(qc, "invalidateQueries");
  latest().onopen?.();
  expect(stream.status()).toBe("live");
  expect(spy).toHaveBeenCalledWith({ type: "active" });
});

// What a call or hook event does to the cache is cache.test.ts's; these
// only check that the events get there.
test("a call event reaches the cache", () => {
  const h: Hook = fixtures.hook;
  const old: Summary = fixtures.calls.at(-1)!;
  qc.setQueryData(keys.calls(h.id), [old]);
  stream.setUser(true);
  latest().emit("call", {
    ...old,
    id: "c_new",
    receivedAt: new Date(Date.parse(old.receivedAt) + 1000).toISOString(),
  });
  expect(qc.getQueryData<Summary[]>(keys.calls(h.id))!.map((c) => c.id)).toEqual(["c_new", old.id]);
});

test("a hook event reaches the cache", () => {
  stream.setUser(true);
  const spy = vi.spyOn(qc, "invalidateQueries");
  latest().emit("hook", { id: "h_1" });
  expect(spy).toHaveBeenCalledWith({ queryKey: keys.hook("h_1") });
});

test("malformed events are ignored", () => {
  stream.setUser(true);
  for (const fn of latest().listeners["call"] ?? [])
    fn(new MessageEvent("call", { data: "not json" }));
  latest().emit("call", { id: 1 });
  expect(stream.status()).not.toBe("off");
});

const problem = (status: number, detail: string) => async () =>
  Response.json(
    { status, detail },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );

test("a stream the relay refuses (403) stops retrying and says why", async () => {
  probe.mockImplementation(problem(403, "event streams need a relay role"));
  stream.setUser(true);
  const spy = vi.spyOn(qc, "invalidateQueries");
  latest().onerror?.();
  await vi.advanceTimersByTimeAsync(0);
  expect(stream.snapshot()).toEqual({
    status: "refused",
    detail: "event streams need a relay role",
  });
  expect(spy).toHaveBeenCalledWith({ type: "active" }); // the viewer may have lost their role
  const before = FakeSource.all.length;
  await vi.advanceTimersByTimeAsync(60_000);
  expect(FakeSource.all).toHaveLength(before);
});

test("too many streams (429) keeps retrying and says why", async () => {
  probe.mockImplementation(problem(429, "this node has too many event streams open"));
  stream.setUser(true);
  latest().onerror?.();
  await vi.advanceTimersByTimeAsync(0);
  expect(stream.snapshot()).toEqual({ status: "crowded" });
  await vi.advanceTimersByTimeAsync(1000);
  expect(open()).toHaveLength(1);
});

test("other failures reconnect without a reason", async () => {
  probe.mockImplementation(async () => new Response(null, { status: 502 }));
  stream.setUser(true);
  latest().onerror?.();
  await vi.advanceTimersByTimeAsync(0);
  expect(stream.snapshot()).toEqual({ status: "reconnecting" });
});

test("the status check asks for the stream and lets go of it at once", async () => {
  stream.setUser(true);
  latest().onerror?.();
  await vi.advanceTimersByTimeAsync(0);
  const [url, init] = probe.mock.calls[0]!;
  expect(url).toBe("/hooks");
  expect(new Headers(init?.headers).get("Accept")).toBe("text/event-stream");
  expect(init?.signal?.aborted).toBe(true);
});

test("a status check that arrives after the stream is back changes nothing", async () => {
  let answer!: (r: Response) => void;
  probe.mockImplementation(() => new Promise((resolve) => (answer = resolve)));
  stream.setUser(true);
  latest().onerror?.();
  await vi.advanceTimersByTimeAsync(1000);
  latest().onopen?.();
  answer(new Response(JSON.stringify({ detail: "x" }), { status: 403 }));
  await vi.advanceTimersByTimeAsync(0);
  expect(stream.snapshot()).toEqual({ status: "live" });
});
