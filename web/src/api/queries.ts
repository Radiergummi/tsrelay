import { useQuery } from "@tanstack/react-query";
import { paths, request, requestText } from "./client";
import * as s from "./schemas";

export const keys = {
  me: ["me"] as const,
  /** allHooks prefixes both hook lists, the viewer's and ?all=true. */
  allHooks: ["hooks"] as const,
  hooks: (all: boolean) => ["hooks", all ? "all" : "mine"] as const,
  hook: (id: string) => ["hook", id] as const,
  calls: (id: string) => ["calls", id] as const,
  call: (hookId: string, callId: string) => ["call", hookId, callId] as const,
  licenses: ["licenses"] as const,
  licenseText: (textId: string) => ["licenseText", textId] as const,
};

export function useMe() {
  return useQuery({
    queryKey: keys.me,
    queryFn: ({ signal }) => request(s.me, "GET", paths.me, undefined, signal),
    staleTime: 60_000,
  });
}

export function useHooks(all: boolean) {
  return useQuery({
    queryKey: keys.hooks(all),
    queryFn: ({ signal }) => request(s.hooks, "GET", paths.hooks(all), undefined, signal),
  });
}

export function useHook(id: string) {
  return useQuery({
    queryKey: keys.hook(id),
    queryFn: ({ signal }) => request(s.hook, "GET", paths.hook(id), undefined, signal),
  });
}

export function useCalls(id: string) {
  return useQuery({
    queryKey: keys.calls(id),
    queryFn: ({ signal }) => request(s.summaries, "GET", paths.calls(id), undefined, signal),
  });
}

export function useCall(hookId: string, callId: string | undefined) {
  return useQuery({
    queryKey: keys.call(hookId, callId ?? ""),
    queryFn: ({ signal }) => request(s.call, "GET", paths.call(hookId, callId!), undefined, signal),
    enabled: callId !== undefined,
  });
}

/** useLicenses fetches the licenses document, which is built into the relay. */
export function useLicenses() {
  return useQuery({
    queryKey: keys.licenses,
    queryFn: ({ signal }) => request(s.licenses, "GET", paths.licenses, undefined, signal),
    staleTime: Infinity,
  });
}

/** useLicenseText fetches one license or notice text once enabled. Its ID is a hash of it. */
export function useLicenseText(textId: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.licenseText(textId),
    queryFn: ({ signal }) => requestText(paths.licenseText(textId), signal),
    enabled,
    staleTime: Infinity,
  });
}
