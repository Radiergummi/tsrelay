import { useEffect, useState } from "react";
import { absoluteTime, relativeTime } from "@/lib/format";

let now = Date.now();
const subscribers = new Set<() => void>();
setInterval(() => {
  now = Date.now();
  subscribers.forEach((fn) => fn());
}, 30_000);

/** TimeAgo shows a relative time that stays current, with the absolute time as its title. */
export function TimeAgo({ iso }: { iso: string }) {
  const [, tick] = useState(0);
  useEffect(() => {
    const fn = () => tick((n) => n + 1);
    subscribers.add(fn);
    return () => void subscribers.delete(fn);
  }, []);
  return (
    <time
      dateTime={iso}
      title={absoluteTime(iso)}
      className="whitespace-nowrap text-muted-foreground"
    >
      {/* oxlint-disable-next-line react/purity -- Date.now() bounds staleness between the 30s ticks */}
      {relativeTime(iso, Math.max(now, Date.now() - 30_000))}
    </time>
  );
}
