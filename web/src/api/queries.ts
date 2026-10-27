import { skipToken, useQuery } from "@tanstack/react-query";
import { keys } from "./cache";
import { paths, request, requestText } from "./client";
import * as s from "./schemas";

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
    queryFn: async ({ signal }) =>
      (await request(s.hookList, "GET", paths.hooks(all, channel), undefined, signal)).hooks,
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
    queryFn: async ({ signal }) =>
      (await request(s.callList, "GET", paths.calls(id), undefined, signal)).calls,
  });
}

export function useCall(hookId: string, callId: string | undefined) {
  return useQuery({
    queryKey: keys.call(hookId, callId ?? ""),
    queryFn:
      callId === undefined
        ? skipToken
        : ({ signal }) => request(s.call, "GET", paths.call(hookId, callId), undefined, signal),
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
