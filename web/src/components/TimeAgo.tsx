import { useEffect, useState } from "react";
import { absoluteTime, relativeTime } from "@/lib/format";

// One shared clock for every mounted TimeAgo, keyed by its re-render
// callback, valued by its timestamp. It ticks every second while any of
// them still counts seconds (relativeTime shows those up to 30 s), and
// otherwise only when the next one crosses a whole minute of age.
const subscribers = new Map<() => void, number>();
let timer: ReturnType<typeof setTimeout> | undefined;

function schedule() {
  clearTimeout(timer);
  if (subscribers.size === 0) return;
  const now = Date.now();
  const ages = [...subscribers.values()].map((t) => Math.max(0, now - t));
  const delay = ages.some((age) => age < 31_000)
    ? 1000
    : Math.min(...ages.map((age) => 60_000 - (age % 60_000)));
  timer = setTimeout(() => {
    subscribers.forEach((_, fn) => fn());
    schedule();
  }, delay);
}

/** TimeAgo shows a relative time that stays current, with the absolute time as its title. */
export function TimeAgo({ iso }: { iso: string }) {
  const [, tick] = useState(0);
  useEffect(() => {
    const fn = () => tick((n) => n + 1);
    subscribers.set(fn, Date.parse(iso));
    schedule();
    return () => {
      subscribers.delete(fn);
      schedule();
    };
  }, [iso]);
  return (
    <time
      dateTime={iso}
      title={absoluteTime(iso)}
      className="whitespace-nowrap text-muted-foreground"
    >
      {/* oxlint-disable-next-line react/purity -- the shared clock re-renders when this changes */}
      {relativeTime(iso, Date.now())}
    </time>
  );
}
