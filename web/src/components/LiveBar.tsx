import { useLiveStatus } from "@/lib/live";

export function LiveBar() {
  if (useLiveStatus() !== "reconnecting") return null;
  return (
    <output className="block border-b bg-muted px-6 py-1.5 text-center text-muted-foreground">
      Live updates paused, reconnecting…
    </output>
  );
}
