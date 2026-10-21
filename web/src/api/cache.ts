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
function refreshHookLists(qc: QueryClient): void {
  void qc.invalidateQueries({ queryKey: keys.allHooks });
  void qc.invalidateQueries({ queryKey: keys.allChannels });
  void qc.invalidateQueries({ queryKey: keys.me });
}

/** hookSaved caches a hook the relay just answered with (create, update, rotate). */
export function hookSaved(qc: QueryClient, hook: Hook): void {
  qc.setQueryData(keys.hook(hook.id), hook);
  refreshHookLists(qc);
}

/** hookChanged refetches a hook that changed elsewhere (or was deleted). */
export function hookChanged(qc: QueryClient, id: string): void {
  void qc.invalidateQueries({ queryKey: keys.hook(id) });
  refreshHookLists(qc);
}

/** hookDeleted drops a hook the viewer deleted, with its calls. */
export function hookDeleted(qc: QueryClient, id: string): void {
  qc.removeQueries({ queryKey: keys.hook(id) });
  qc.removeQueries({ queryKey: keys.calls(id) });
  refreshHookLists(qc);
}

const newer = (s: Summary, last: Summary | undefined) =>
  !last || last.id === s.id || Date.parse(s.receivedAt) >= Date.parse(last.receivedAt);

/**
 * fold puts a call summary into the cached list and the hook's lastCall. The
 * list keeps no more calls than the relay does (the entry document's
 * policy.callsPerHook). A list being fetched is refetched instead, since the
 * answer in flight may predate the call and would overwrite it.
 */
function fold(qc: QueryClient, s: Summary): void {
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
  qc.setQueriesData<Hook[]>({ queryKey: keys.allHooks }, (list) => list?.map(withLast));
  qc.setQueryData<Hook>(keys.hook(s.hookId), (h) => (h ? withLast(h) : h));
}

/** callRecorded folds in a call the event stream reported, and refreshes the call if it's open. */
export function callRecorded(qc: QueryClient, s: Summary): void {
  fold(qc, s);
  void qc.invalidateQueries({ queryKey: keys.call(s.hookId, s.id) });
}

/** callReturned caches a call a replay or test answered with, whole, and folds it in. */
export function callReturned(qc: QueryClient, c: Call): void {
  qc.setQueryData(keys.call(c.hookId, c.id), c);
  fold(qc, summary.parse(c));
}

/** resync refetches whatever is on screen, after events may have been missed. */
export function resync(qc: QueryClient): void {
  void qc.invalidateQueries({ type: "active" });
}
