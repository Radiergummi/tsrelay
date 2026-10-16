import { useQuery } from "@tanstack/react-query";
import { paths, request } from "./client";
import * as s from "./schemas";

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
};

export function useMe() {
  return useQuery({
    queryKey: keys.me,
    queryFn: ({ signal }) => request(s.me, "GET", paths.me, undefined, signal),
    staleTime: 60_000,
  });
}

export function useHooks(all: boolean, channel?: string) {
  return useQuery({
    queryKey: keys.hooks(all, channel),
    queryFn: ({ signal }) => request(s.hooks, "GET", paths.hooks(all, channel), undefined, signal),
  });
}

export function useChannels(all: boolean) {
  return useQuery({
    queryKey: keys.channels(all),
    queryFn: ({ signal }) => request(s.channels, "GET", paths.channels(all), undefined, signal),
  });
}

export function useChannel(name: string, all: boolean) {
  return useQuery({
    queryKey: keys.channel(name, all),
    queryFn: ({ signal }) =>
      request(s.channelDetail, "GET", paths.channel(name, all), undefined, signal),
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
