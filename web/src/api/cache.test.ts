import { QueryClient } from "@tanstack/react-query";
import { beforeEach, expect, test, vi } from "vitest";
import type { Call, Hook, Me, Summary } from "@/api/schemas";
import { fixtures } from "@/test/fixtures";
import { callRecorded, callReturned, hookChanged, hookDeleted, hookSaved, keys } from "./cache";

let qc: QueryClient;
beforeEach(() => {
  qc = new QueryClient();
});

const h: Hook = fixtures.hook;
const base: Summary = fixtures.calls.at(-1)!;
const later = (s: Summary, seconds: number, id: string): Summary => ({
  ...s,
  id,
  receivedAt: new Date(Date.parse(s.receivedAt) + seconds * 1000).toISOString(),
});
const returned = (seconds: number, id: string): Call => ({
  ...fixtures.callReplay,
  ...later(base, seconds, id),
  hookId: h.id,
});
const ids = () => qc.getQueryData<Summary[]>(keys.calls(h.id))!.map((c) => c.id);
const atCap = (cap: number) =>
  qc.setQueryData<Me>(keys.me, {
    ...fixtures.me,
    policy: { ...fixtures.me.policy, callsPerHook: cap },
  });
const fetching = () => {
  void qc.fetchQuery({
    queryKey: keys.calls(h.id),
    queryFn: () => new Promise<Summary[]>(() => {}),
  });
  expect(qc.getQueryState(keys.calls(h.id))?.fetchStatus).toBe("fetching");
};
const expectListsRefreshed = (spy: ReturnType<typeof vi.spyOn>) => {
  expect(spy).toHaveBeenCalledWith({ queryKey: keys.allHooks });
  expect(spy).toHaveBeenCalledWith({ queryKey: keys.allChannels });
  expect(spy).toHaveBeenCalledWith({ queryKey: keys.me });
};

test("a recorded call updates the list, the hook's lastCall and the open call", () => {
  qc.setQueryData(keys.calls(h.id), [base]);
  qc.setQueryData(keys.hooks(false), [{ ...h, lastCall: base }]);
  qc.setQueryData(keys.hook(h.id), { ...h, lastCall: base });
  const spy = vi.spyOn(qc, "invalidateQueries");

  const fresh = { ...later(base, 1, "c_new"), outcome: "pending" };
  callRecorded(qc, fresh);
  expect(ids()).toEqual(["c_new", base.id]);
  expect(qc.getQueryData<Hook[]>(keys.hooks(false))![0]!.lastCall?.id).toBe("c_new");
  expect(qc.getQueryData<Hook>(keys.hook(h.id))!.lastCall?.id).toBe("c_new");

  callRecorded(qc, { ...fresh, outcome: "ok", status: 200 }); // completion replaces, not appends
  expect(ids()).toEqual(["c_new", base.id]);
  expect(qc.getQueryData<Summary[]>(keys.calls(h.id))![0]!.status).toBe(200);
  expect(spy).toHaveBeenCalledWith({ queryKey: keys.call(h.id, "c_new") });
});

test("a list at the relay's cap stays at the cap, dropping the oldest", () => {
  atCap(3);
  qc.setQueryData(keys.calls(h.id), [
    later(base, 2, "c_2"),
    later(base, 1, "c_1"),
    later(base, 0, "c_0"),
  ]);
  callRecorded(qc, later(base, 3, "c_3"));
  expect(ids()).toEqual(["c_3", "c_2", "c_1"]);
});

// A fetch that started before the call would answer without it and
// overwrite the patched list, so the list is refetched instead.
test("a recorded call while the list is being fetched refetches it rather than patching", () => {
  qc.setQueryData(keys.calls(h.id), [base]);
  fetching();
  const spy = vi.spyOn(qc, "invalidateQueries");
  callRecorded(qc, later(base, 1, "c_new"));
  expect(spy).toHaveBeenCalledWith({ queryKey: keys.calls(h.id) });
  expect(ids()).toEqual([base.id]);
});

test("a changed hook refetches the hook, the lists, the channels and the entry document", () => {
  const spy = vi.spyOn(qc, "invalidateQueries");
  hookChanged(qc, "h_1");
  expect(spy).toHaveBeenCalledWith({ queryKey: keys.hook("h_1") });
  expectListsRefreshed(spy);
});

test("a saved hook is cached and refreshes the lists, the channels and the entry document", () => {
  const spy = vi.spyOn(qc, "invalidateQueries");
  hookSaved(qc, h);
  expect(qc.getQueryData(keys.hook(h.id))).toEqual(h);
  expectListsRefreshed(spy);
});

test("a deleted hook drops the hook and its calls", () => {
  qc.setQueryData(keys.hook(h.id), h);
  qc.setQueryData(keys.calls(h.id), [base]);
  const spy = vi.spyOn(qc, "invalidateQueries");
  hookDeleted(qc, h.id);
  expect(qc.getQueryState(keys.hook(h.id))).toBeUndefined();
  expect(qc.getQueryState(keys.calls(h.id))).toBeUndefined();
  expectListsRefreshed(spy);
});

// Replay and test answer with the whole call, so refetching it would only
// cost a GET (and a 404 there would show the new call as gone).
test("a returned call is cached and folded in without refetching it", () => {
  qc.setQueryData(keys.calls(h.id), [base]);
  qc.setQueryData(keys.hooks(false), [{ ...h, lastCall: base }]);
  qc.setQueryData(keys.hook(h.id), { ...h, lastCall: base });
  const spy = vi.spyOn(qc, "invalidateQueries");
  const c = returned(1, "c_new");
  callReturned(qc, c);
  expect(qc.getQueryData(keys.call(h.id, "c_new"))).toEqual(c);
  expect(ids()).toEqual(["c_new", base.id]);
  expect(qc.getQueryData<Summary[]>(keys.calls(h.id))![0]).not.toHaveProperty("responseBody");
  expect(qc.getQueryData<Hook[]>(keys.hooks(false))![0]!.lastCall?.id).toBe("c_new");
  expect(qc.getQueryData<Hook>(keys.hook(h.id))!.lastCall?.id).toBe("c_new");
  expect(spy).not.toHaveBeenCalledWith({ queryKey: keys.call(h.id, "c_new") });
});

test("a returned call respects the relay's cap", () => {
  atCap(2);
  qc.setQueryData(keys.calls(h.id), [later(base, 1, "c_1"), later(base, 0, "c_0")]);
  callReturned(qc, returned(2, "c_2"));
  expect(ids()).toEqual(["c_2", "c_1"]);
});

test("a returned call while the list is being fetched refetches the list instead", () => {
  qc.setQueryData(keys.calls(h.id), [base]);
  fetching();
  const spy = vi.spyOn(qc, "invalidateQueries");
  callReturned(qc, returned(1, "c_new"));
  expect(spy).toHaveBeenCalledWith({ queryKey: keys.calls(h.id) });
  expect(ids()).toEqual([base.id]);
});
