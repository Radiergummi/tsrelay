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
  stream = new LiveStream(qc, {
    EventSource: FakeSource as never,
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

test("a call event updates the list, the hook's lastCall and the open call", () => {
  const h: Hook = fixtures.hook;
  const old: Summary = fixtures.calls.at(-1)!;
  qc.setQueryData(keys.calls(h.id), [old]);
  qc.setQueryData(keys.hooks(false), [{ ...h, lastCall: old }]);
  qc.setQueryData(keys.hook(h.id), { ...h, lastCall: old });
  qc.setQueryData(keys.call(h.id, "c_new"), fixtures.callPending);
  const spy = vi.spyOn(qc, "invalidateQueries");
  stream.setUser(true);

  const fresh: Summary = {
    ...old,
    id: "c_new",
    receivedAt: new Date(Date.parse(old.receivedAt) + 1000).toISOString(),
    outcome: "pending",
  };
  latest().emit("call", fresh);
  expect(qc.getQueryData<Summary[]>(keys.calls(h.id))!.map((c) => c.id)).toEqual(["c_new", old.id]);
  expect(qc.getQueryData<Hook[]>(keys.hooks(false))![0]!.lastCall?.id).toBe("c_new");
  expect(qc.getQueryData<Hook>(keys.hook(h.id))!.lastCall?.id).toBe("c_new");

  latest().emit("call", { ...fresh, outcome: "ok", status: 200 }); // completion replaces, not appends
  expect(qc.getQueryData<Summary[]>(keys.calls(h.id))!.map((c) => c.id)).toEqual(["c_new", old.id]);
  expect(qc.getQueryData<Summary[]>(keys.calls(h.id))![0]!.status).toBe(200);
  expect(spy).toHaveBeenCalledWith({ queryKey: keys.call(h.id, "c_new") });
});

test("a hook event invalidates the hook, the lists and the entry document", () => {
  stream.setUser(true);
  const spy = vi.spyOn(qc, "invalidateQueries");
  latest().emit("hook", { id: "h_1" });
  expect(spy).toHaveBeenCalledWith({ queryKey: keys.hook("h_1") });
  expect(spy).toHaveBeenCalledWith({ queryKey: ["hooks"] });
  expect(spy).toHaveBeenCalledWith({ queryKey: keys.me });
});

test("malformed events are ignored", () => {
  stream.setUser(true);
  for (const fn of latest().listeners["call"] ?? [])
    fn(new MessageEvent("call", { data: "not json" }));
  latest().emit("call", { id: 1 });
  expect(stream.status()).not.toBe("off");
});
