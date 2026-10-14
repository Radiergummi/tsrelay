import type { QueryClient } from "@tanstack/react-query";
import {
  createContext,
  useContext,
  useEffect,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from "react";
import { LiveStream, type LiveStatus } from "./eventStream";

const Live = createContext<LiveStream | null>(null);

/** LiveProvider owns the tab's one event stream. user: the viewer is a user (tagged nodes get no events). */
export function LiveProvider({
  client,
  user,
  children,
}: {
  client: QueryClient;
  user: boolean;
  children: ReactNode;
}) {
  const [stream] = useState(() => new LiveStream(client));
  useEffect(() => {
    stream.start();
    return () => stream.stop();
  }, [stream]);
  useEffect(() => stream.setUser(user), [stream, user]);
  return <Live.Provider value={stream}>{children}</Live.Provider>;
}

export function useLiveStatus(): LiveStatus {
  const stream = useContext(Live);
  return useSyncExternalStore(
    (fn) => stream?.subscribe(fn) ?? (() => {}),
    () => stream?.status() ?? "off",
  );
}

/** useAllEvents switches the stream to every hook's events while enabled (admin pages). */
export function useAllEvents(enabled: boolean): void {
  const stream = useContext(Live);
  useEffect(() => (enabled && stream ? stream.acquireAll() : undefined), [stream, enabled]);
}
