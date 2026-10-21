import { useMutation, useQueryClient } from "@tanstack/react-query";
import { callReturned, hookDeleted, hookSaved } from "./cache";
import { paths, request, requestVoid } from "./client";
import * as s from "./schemas";

export function useCreateHook() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: s.HookRequest) => request(s.hook, "POST", paths.hooks(), body),
    onSuccess: (h) => hookSaved(qc, h),
  });
}

export function useUpdateHook(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: s.HookRequest) => request(s.hook, "PATCH", paths.hook(id), body),
    onSuccess: (h) => hookSaved(qc, h),
  });
}

/** useRotatePublicUrl gives a public hook a new public URL; the old one stops working. */
export function useRotatePublicUrl(hookId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => request(s.hook, "POST", paths.rotatePublicUrl(hookId)),
    onSuccess: (h) => hookSaved(qc, h),
  });
}

export function useDeleteHook() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => requestVoid("DELETE", paths.hook(id)),
    onSuccess: (_, id) => hookDeleted(qc, id),
  });
}

/** useReplay replays a call; it resolves to the new call. */
export function useReplay(hookId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (callId: string) => request(s.call, "POST", paths.replay(hookId, callId)),
    onSuccess: (c) => callReturned(qc, c),
  });
}

/** useTestRequest sends a test request; it resolves to the new call. */
export function useTestRequest(hookId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: s.TestRequest) => request(s.call, "POST", paths.test(hookId), req),
    onSuccess: (c) => callReturned(qc, c),
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
