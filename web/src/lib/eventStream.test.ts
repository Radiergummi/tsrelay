import { QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { keys } from "@/api/cache";
import type { Hook, Summary } from "@/api/schemas";
import { fixtures } from "@/test/fixtures";
import { LiveStream } from "./eventStream";

class FakeSource {
  static all: FakeSource[] = [];
  listeners: Record<string, ((event: MessageEvent) => void)[]> = {};
  closed = false;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  url: string;
  constructor(url: string) {
    this.url = url;
    FakeSource.all.push(this);
  }
  addEventListener(type: string, listener: (event: MessageEvent) => void) {
    (this.listeners[type] ??= []).push(listener);
  }
  close() {
    this.closed = true;
  }
  emit(type: string, data: unknown) {
    for (const listener of this.listeners[type] ?? [])
      listener(new MessageEvent(type, { data: JSON.stringify(data) }));
  }
}
const open = () => FakeSource.all.filter((source) => !source.closed);
const latest = () => FakeSource.all.at(-1)!;

let fakeDocument: {
  hidden: boolean;
  listeners: (() => void)[];
  addEventListener: (type: string, listener: () => void) => void;
  removeEventListener: () => void;
};
let queryClient: QueryClient;
let stream: LiveStream;
// probe answers the status check after a failed connect; a 200 by default.
let probe: ReturnType<typeof vi.fn<typeof fetch>>;

beforeEach(() => {
  vi.useFakeTimers();
  FakeSource.all = [];
  fakeDocument = {
    hidden: false,
    listeners: [],
    addEventListener: (_, listener) => fakeDocument.listeners.push(listener),
    removeEventListener: () => {},
  };
  queryClient = new QueryClient();
  probe = vi.fn<typeof fetch>(async () => new Response(null, { status: 200 }));
  stream = new LiveStream(queryClient, {
    EventSource: FakeSource as never,
    fetch: probe,
    document: fakeDocument as never,
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
  expect(open().map((source) => source.url)).toEqual(["/hooks?all=true"]);
  release();
  expect(open()).toHaveLength(0);
  stream.setUser(true);
  expect(open().map((source) => source.url)).toEqual(["/hooks"]);
  const r2 = stream.acquireAll();
  expect(open().map((source) => source.url)).toEqual(["/hooks?all=true"]);
  r2();
  expect(open().map((source) => source.url)).toEqual(["/hooks"]);
});

test("closed while hidden; reopening invalidates the active queries", () => {
  stream.setUser(true);
  latest().onopen?.();
  const spy = vi.spyOn(queryClient, "invalidateQueries");
  fakeDocument.hidden = true;
  fakeDocument.listeners.forEach((listener) => listener());
  expect(open()).toHaveLength(0);
  fakeDocument.hidden = false;
  fakeDocument.listeners.forEach((listener) => listener());
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
  const spy = vi.spyOn(queryClient, "invalidateQueries");
  latest().onopen?.();
  expect(stream.status()).toBe("live");
  expect(spy).toHaveBeenCalledWith({ type: "active" });
});

// What a call or hook event does to the cache is cache.test.ts's; these
// only check that the events get there.
test("a call event reaches the cache", () => {
  const hook: Hook = fixtures.hook;
  const old: Summary = fixtures.calls.at(-1)!;
  queryClient.setQueryData(keys.calls(hook.id), [old]);
  stream.setUser(true);
  latest().emit("call", {
    ...old,
    id: "c_new",
    receivedAt: new Date(Date.parse(old.receivedAt) + 1000).toISOString(),
  });
  expect(queryClient.getQueryData<Summary[]>(keys.calls(hook.id))!.map((call) => call.id)).toEqual([
    "c_new",
    old.id,
  ]);
});

test("a hook event reaches the cache", () => {
  stream.setUser(true);
  const spy = vi.spyOn(queryClient, "invalidateQueries");
  latest().emit("hook", { id: "h_1" });
  expect(spy).toHaveBeenCalledWith({ queryKey: keys.hook("h_1") });
});

test("malformed events are ignored", () => {
  stream.setUser(true);
  for (const listener of latest().listeners["call"] ?? [])
    listener(new MessageEvent("call", { data: "not json" }));
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
  const spy = vi.spyOn(queryClient, "invalidateQueries");
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
  await vi.advanceTimersByTimeAsync(1250); // the first backoff, at its most jittered
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
  await vi.advanceTimersByTimeAsync(1250); // the first backoff, at its most jittered
  expect(open()).toHaveLength(1);
  latest().onopen?.();
  answer(new Response(JSON.stringify({ detail: "x" }), { status: 403 }));
  await vi.advanceTimersByTimeAsync(0);
  expect(stream.snapshot()).toEqual({ status: "live" });
});

test("a status check that arrives after the stream stopped changes nothing", async () => {
  let answer!: (r: Response) => void;
  probe.mockImplementation(() => new Promise((resolve) => (answer = resolve)));
  stream.setUser(true);
  latest().onerror?.();
  stream.setUser(false); // e.g. the viewer lost their role
  const spy = vi.spyOn(queryClient, "invalidateQueries");
  answer(Response.json({ detail: "event streams need a relay role" }, { status: 403 }));
  await vi.advanceTimersByTimeAsync(0);
  expect(stream.snapshot()).toEqual({ status: "off" });
  expect(spy).not.toHaveBeenCalled();
});
