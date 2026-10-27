import { useMutation, useQueryClient } from "@tanstack/react-query";
import { callReturned, hookDeleted, hookSaved } from "./cache";
import { paths, request, requestVoid } from "./client";
import * as schemas from "./schemas";

export function useCreateHook() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: schemas.HookRequest) => request(schemas.hook, "POST", paths.hooks(), body),
    onSuccess: (hook) => hookSaved(queryClient, hook),
  });
}

export function useUpdateHook(id: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: schemas.HookRequest) => request(schemas.hook, "PATCH", paths.hook(id), body),
    onSuccess: (hook) => hookSaved(queryClient, hook),
  });
}

/** useRotatePublicUrl gives a public hook a new public URL; the old one stops working. */
export function useRotatePublicUrl(hookId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => request(schemas.hook, "POST", paths.rotatePublicUrl(hookId)),
    onSuccess: (hook) => hookSaved(queryClient, hook),
  });
}

export function useDeleteHook() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => requestVoid("DELETE", paths.hook(id)),
    onSuccess: (_, id) => hookDeleted(queryClient, id),
  });
}

/** useReplay replays a call; it resolves to the new call. */
export function useReplay(hookId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (callId: string) => request(schemas.call, "POST", paths.replay(hookId, callId)),
    onSuccess: (call) => callReturned(queryClient, call),
  });
}

/** useTestRequest sends a test request; it resolves to the new call. */
export function useTestRequest(hookId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: schemas.TestRequest) =>
      request(schemas.call, "POST", paths.test(hookId), body),
    onSuccess: (call) => callReturned(queryClient, call),
  });
}

/** dryRunHook validates a create (no editingId) or an update without saving. */
export function dryRunHook(
  body: schemas.HookRequest,
  editingId: string | undefined,
  signal: AbortSignal,
): Promise<schemas.Hook> {
  const path = (editingId ? paths.hook(editingId) : paths.hooks()) + "?dryRun=true";
  return request(schemas.hook, editingId ? "PATCH" : "POST", path, body, signal);
}
