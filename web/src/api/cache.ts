import type { QueryClient } from "@tanstack/react-query";
import { summary, type Call, type Hook, type Me, type Summary } from "./schemas";

/**
 * cache owns the query keys and what a change means for them. Mutations and
 * the event stream report what happened here; nothing else touches the keys
 * of the cache directly.
 */
export const keys = {
  me: ["me"] as const,
  /** allHooks prefixes both hook lists, the viewer's and ?all=true. */
  allHooks: ["hooks"] as const,
  hooks: (all: boolean, channel?: string) =>
    channel
      ? (["hooks", all ? "all" : "mine", channel] as const)
      : (["hooks", all ? "all" : "mine"] as const),
  /** allChannels prefixes the channel list and every channel, in either scope. */
  allChannels: ["channels"] as const,
  channels: (all: boolean) => ["channels", all ? "all" : "mine"] as const,
  channel: (name: string, all: boolean) => ["channels", all ? "all" : "mine", name] as const,
  hook: (id: string) => ["hook", id] as const,
  calls: (id: string) => ["calls", id] as const,
  call: (hookId: string, callId: string) => ["call", hookId, callId] as const,
  licenses: ["licenses"] as const,
  licenseText: (textId: string) => ["licenseText", textId] as const,
};

/** refreshHookLists refetches what a hook change can alter: the lists, the channels and the entry document's hook count. */
function refreshHookLists(queryClient: QueryClient): void {
  void queryClient.invalidateQueries({ queryKey: keys.allHooks });
  void queryClient.invalidateQueries({ queryKey: keys.allChannels });
  void queryClient.invalidateQueries({ queryKey: keys.me });
}

/** hookSaved caches a hook the relay just answered with (create, update, rotate). */
export function hookSaved(queryClient: QueryClient, hook: Hook): void {
  queryClient.setQueryData(keys.hook(hook.id), hook);
  refreshHookLists(queryClient);
}

/** hookChanged refetches a hook that changed elsewhere (or was deleted). */
export function hookChanged(queryClient: QueryClient, id: string): void {
  void queryClient.invalidateQueries({ queryKey: keys.hook(id) });
  refreshHookLists(queryClient);
}

/** hookDeleted drops a hook the viewer deleted, with its calls. */
export function hookDeleted(queryClient: QueryClient, id: string): void {
  queryClient.removeQueries({ queryKey: keys.hook(id) });
  queryClient.removeQueries({ queryKey: keys.calls(id) });
  refreshHookLists(queryClient);
}

const newer = (call: Summary, last: Summary | undefined) =>
  !last || last.id === call.id || Date.parse(call.receivedAt) >= Date.parse(last.receivedAt);

/**
 * fold puts a call summary into the cached list and the hook's lastCall. The
 * list keeps no more calls than the relay does (the entry document's
 * policy.callsPerHook). A list being fetched is refetched instead, since the
 * answer in flight may predate the call and would overwrite it.
 */
function fold(queryClient: QueryClient, call: Summary): void {
  const key = keys.calls(call.hookId);
  if (queryClient.getQueryState(key)?.fetchStatus === "fetching") {
    void queryClient.invalidateQueries({ queryKey: key });
  } else {
    const cap = queryClient.getQueryData<Me>(keys.me)?.policy.callsPerHook;
    queryClient.setQueryData<Summary[]>(key, (list) => {
      if (!list) return list;
      const rest = list.filter(({ id }) => id !== call.id);
      const sorted = [call, ...rest].sort(
        (a, b) => Date.parse(b.receivedAt) - Date.parse(a.receivedAt),
      );
      return cap === undefined ? sorted : sorted.slice(0, cap);
    });
  }
  const withLast = (hook: Hook): Hook =>
    hook.id === call.hookId && newer(call, hook.lastCall) ? { ...hook, lastCall: call } : hook;
  queryClient.setQueriesData<Hook[]>({ queryKey: keys.allHooks }, (list) => list?.map(withLast));
  queryClient.setQueryData<Hook>(keys.hook(call.hookId), (hook) => (hook ? withLast(hook) : hook));
}

/** callRecorded folds in a call the event stream reported, and refreshes the call if it's open. */
export function callRecorded(queryClient: QueryClient, call: Summary): void {
  fold(queryClient, call);
  void queryClient.invalidateQueries({ queryKey: keys.call(call.hookId, call.id) });
}

/** callReturned caches a call a replay or test answered with, whole, and folds it in. */
export function callReturned(queryClient: QueryClient, call: Call): void {
  queryClient.setQueryData(keys.call(call.hookId, call.id), call);
  fold(queryClient, summary.parse(call));
}

/** resync refetches whatever is on screen, after events may have been missed. */
export function resync(queryClient: QueryClient): void {
  void queryClient.invalidateQueries({ type: "active" });
}
