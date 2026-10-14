import { useMutation, useQueryClient } from "@tanstack/react-query";
import { paths, request, requestVoid } from "./client";
import { keys } from "./queries";
import * as s from "./schemas";

export function useCreateHook() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: s.HookRequest) => request(s.hook, "POST", paths.hooks(), body),
    onSuccess: (h) => {
      qc.setQueryData(keys.hook(h.id), h);
      void qc.invalidateQueries({ queryKey: keys.allHooks });
      void qc.invalidateQueries({ queryKey: keys.me });
    },
  });
}

export function useUpdateHook(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: s.HookRequest) => request(s.hook, "PATCH", paths.hook(id), body),
    onSuccess: (h) => {
      qc.setQueryData(keys.hook(h.id), h);
      void qc.invalidateQueries({ queryKey: keys.allHooks });
    },
  });
}

export function useDeleteHook() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => requestVoid("DELETE", paths.hook(id)),
    onSuccess: (_, id) => {
      qc.removeQueries({ queryKey: keys.hook(id) });
      qc.removeQueries({ queryKey: keys.calls(id) });
      void qc.invalidateQueries({ queryKey: keys.allHooks });
      void qc.invalidateQueries({ queryKey: keys.me });
    },
  });
}

/** useReplay replays a call; it resolves to the new call. */
export function useReplay(hookId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (callId: string) => request(s.call, "POST", paths.replay(hookId, callId)),
    onSuccess: (c) => {
      qc.setQueryData(keys.call(hookId, c.id), c);
      void qc.invalidateQueries({ queryKey: keys.calls(hookId) });
    },
  });
}

/** useTestRequest sends a test request; it resolves to the new call. */
export function useTestRequest(hookId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: s.TestRequest) => request(s.call, "POST", paths.test(hookId), req),
    onSuccess: (c) => {
      qc.setQueryData(keys.call(hookId, c.id), c);
      void qc.invalidateQueries({ queryKey: keys.calls(hookId) });
    },
  });
}

/** dryRunHook validates a create (no editingId) or an update without saving. */
export function dryRunHook(
  body: s.HookRequest,
  editingId: string | undefined,
  signal: AbortSignal,
): Promise<s.Hook> {
  const path = (editingId ? paths.hook(editingId) : paths.hooks()) + "?dryRun=true";
  return request(s.hook, editingId ? "PATCH" : "POST", path, body, signal);
}
