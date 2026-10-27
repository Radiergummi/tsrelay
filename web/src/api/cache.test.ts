import { QueryClient } from "@tanstack/react-query";
import { beforeEach, expect, test, vi } from "vitest";
import type { Call, Hook, Me, Summary } from "@/api/schemas";
import { fixtures } from "@/test/fixtures";
import { callRecorded, callReturned, hookChanged, hookDeleted, hookSaved, keys } from "./cache";

let queryClient: QueryClient;
beforeEach(() => {
  queryClient = new QueryClient();
});

const hook: Hook = fixtures.hook;
const base: Summary = fixtures.calls.at(-1)!;
const later = (call: Summary, seconds: number, id: string): Summary => ({
  ...call,
  id,
  receivedAt: new Date(Date.parse(call.receivedAt) + seconds * 1000).toISOString(),
});
const returned = (seconds: number, id: string): Call => ({
  ...fixtures.callReplay,
  ...later(base, seconds, id),
  hookId: hook.id,
});
const ids = () => queryClient.getQueryData<Summary[]>(keys.calls(hook.id))!.map(({ id }) => id);
const atCap = (cap: number) =>
  queryClient.setQueryData<Me>(keys.me, {
    ...fixtures.me,
    policy: { ...fixtures.me.policy, callsPerHook: cap },
  });
const fetching = () => {
  void queryClient.fetchQuery({
    queryKey: keys.calls(hook.id),
    queryFn: () => new Promise<Summary[]>(() => {}),
  });
  expect(queryClient.getQueryState(keys.calls(hook.id))?.fetchStatus).toBe("fetching");
};
const expectListsRefreshed = (spy: ReturnType<typeof vi.spyOn>) => {
  expect(spy).toHaveBeenCalledWith({ queryKey: keys.allHooks });
  expect(spy).toHaveBeenCalledWith({ queryKey: keys.allChannels });
  expect(spy).toHaveBeenCalledWith({ queryKey: keys.me });
};

test("a recorded call updates the list, the hook's lastCall and the open call", () => {
  queryClient.setQueryData(keys.calls(hook.id), [base]);
  queryClient.setQueryData(keys.hooks(false), [{ ...hook, lastCall: base }]);
  queryClient.setQueryData(keys.hook(hook.id), { ...hook, lastCall: base });
  const spy = vi.spyOn(queryClient, "invalidateQueries");

  const fresh = { ...later(base, 1, "c_new"), outcome: "pending" };
  callRecorded(queryClient, fresh);
  expect(ids()).toEqual(["c_new", base.id]);
  expect(queryClient.getQueryData<Hook[]>(keys.hooks(false))![0]!.lastCall?.id).toBe("c_new");
  expect(queryClient.getQueryData<Hook>(keys.hook(hook.id))!.lastCall?.id).toBe("c_new");

  callRecorded(queryClient, { ...fresh, outcome: "ok", status: 200 }); // completion replaces, not appends
  expect(ids()).toEqual(["c_new", base.id]);
  expect(queryClient.getQueryData<Summary[]>(keys.calls(hook.id))![0]!.status).toBe(200);
  expect(spy).toHaveBeenCalledWith({ queryKey: keys.call(hook.id, "c_new") });
});

test("a list at the relay's cap stays at the cap, dropping the oldest", () => {
  atCap(3);
  queryClient.setQueryData(keys.calls(hook.id), [
    later(base, 2, "c_2"),
    later(base, 1, "c_1"),
    later(base, 0, "c_0"),
  ]);
  callRecorded(queryClient, later(base, 3, "c_3"));
  expect(ids()).toEqual(["c_3", "c_2", "c_1"]);
});

// A fetch that started before the call would answer without it and
// overwrite the patched list, so the list is refetched instead.
test("a recorded call while the list is being fetched refetches it rather than patching", () => {
  queryClient.setQueryData(keys.calls(hook.id), [base]);
  fetching();
  const spy = vi.spyOn(queryClient, "invalidateQueries");
  callRecorded(queryClient, later(base, 1, "c_new"));
  expect(spy).toHaveBeenCalledWith({ queryKey: keys.calls(hook.id) });
  expect(ids()).toEqual([base.id]);
});

test("a changed hook refetches the hook, the lists, the channels and the entry document", () => {
  const spy = vi.spyOn(queryClient, "invalidateQueries");
  hookChanged(queryClient, "h_1");
  expect(spy).toHaveBeenCalledWith({ queryKey: keys.hook("h_1") });
  expectListsRefreshed(spy);
});

test("a saved hook is cached and refreshes the lists, the channels and the entry document", () => {
  const spy = vi.spyOn(queryClient, "invalidateQueries");
  hookSaved(queryClient, hook);
  expect(queryClient.getQueryData(keys.hook(hook.id))).toEqual(hook);
  expectListsRefreshed(spy);
});

test("a deleted hook drops the hook and its calls", () => {
  queryClient.setQueryData(keys.hook(hook.id), hook);
  queryClient.setQueryData(keys.calls(hook.id), [base]);
  const spy = vi.spyOn(queryClient, "invalidateQueries");
  hookDeleted(queryClient, hook.id);
  expect(queryClient.getQueryState(keys.hook(hook.id))).toBeUndefined();
  expect(queryClient.getQueryState(keys.calls(hook.id))).toBeUndefined();
  expectListsRefreshed(spy);
});

// Replay and test answer with the whole call, so refetching it would only
// cost a GET (and a 404 there would show the new call as gone).
test("a returned call is cached and folded in without refetching it", () => {
  queryClient.setQueryData(keys.calls(hook.id), [base]);
  queryClient.setQueryData(keys.hooks(false), [{ ...hook, lastCall: base }]);
  queryClient.setQueryData(keys.hook(hook.id), { ...hook, lastCall: base });
  const spy = vi.spyOn(queryClient, "invalidateQueries");
  const call = returned(1, "c_new");
  callReturned(queryClient, call);
  expect(queryClient.getQueryData(keys.call(hook.id, "c_new"))).toEqual(call);
  expect(ids()).toEqual(["c_new", base.id]);
  expect(queryClient.getQueryData<Summary[]>(keys.calls(hook.id))![0]).not.toHaveProperty(
    "responseBody",
  );
  expect(queryClient.getQueryData<Hook[]>(keys.hooks(false))![0]!.lastCall?.id).toBe("c_new");
  expect(queryClient.getQueryData<Hook>(keys.hook(hook.id))!.lastCall?.id).toBe("c_new");
  expect(spy).not.toHaveBeenCalledWith({ queryKey: keys.call(hook.id, "c_new") });
});

test("a returned call respects the relay's cap", () => {
  atCap(2);
  queryClient.setQueryData(keys.calls(hook.id), [later(base, 1, "c_1"), later(base, 0, "c_0")]);
  callReturned(queryClient, returned(2, "c_2"));
  expect(ids()).toEqual(["c_2", "c_1"]);
});

test("a returned call while the list is being fetched refetches the list instead", () => {
  queryClient.setQueryData(keys.calls(hook.id), [base]);
  fetching();
  const spy = vi.spyOn(queryClient, "invalidateQueries");
  callReturned(queryClient, returned(1, "c_new"));
  expect(spy).toHaveBeenCalledWith({ queryKey: keys.calls(hook.id) });
  expect(ids()).toEqual([base.id]);
});
