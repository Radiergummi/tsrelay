import { useLiveStatus } from "@/lib/live";

export function LiveBar() {
  const { status, detail } = useLiveStatus();
  const message =
    status === "reconnecting"
      ? "Live updates paused, reconnecting…"
      : status === "crowded"
        ? "Live updates paused: too many live connections from this device. Close other tsrelay tabs; reconnecting…"
        : status === "refused"
          ? `Live updates are off: ${detail}.`
          : null;
  if (!message) return null;
  return (
    <output className="block border-b bg-muted px-6 py-1.5 text-center text-muted-foreground">
      {message}
    </output>
  );
}
