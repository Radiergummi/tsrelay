import { useCallback, useSyncExternalStore } from "react";
import { absoluteTime, relativeTime } from "@/lib/format";

// One shared clock for every mounted TimeAgo, keyed by its change callback,
// valued by its timestamp. It ticks every second while any of them still
// counts seconds (relativeTime shows those up to 30 s), and otherwise only
// when the next one crosses a whole minute of age. Subscribing never pushes
// a pending tick later, so rows mounting faster than once a second cannot
// starve it.
const subscribers = new Map<() => void, number>();
let timer: ReturnType<typeof setTimeout> | undefined;
let due = Infinity;

function schedule() {
  if (subscribers.size === 0) {
    clearTimeout(timer);
    due = Infinity;
    return;
  }
  const now = Date.now();
  const ages = [...subscribers.values()].map((timestamp) => Math.max(0, now - timestamp));
  const delay = ages.some((age) => age < 31_000)
    ? 1000
    : Math.min(...ages.map((age) => 60_000 - (age % 60_000)));
  if (now + delay >= due) return;
  clearTimeout(timer);
  due = now + delay;
  timer = setTimeout(() => {
    due = Infinity;
    subscribers.forEach((_, onChange) => onChange());
    schedule();
  }, delay);
}

/** TimeAgo shows a relative time that stays current, with the absolute time as its title. */
export function TimeAgo({ iso }: { iso: string }) {
  const subscribe = useCallback(
    (onChange: () => void) => {
      subscribers.set(onChange, Date.parse(iso));
      schedule();
      return () => {
        subscribers.delete(onChange);
        schedule();
      };
    },
    [iso],
  );
  // The snapshot is the label itself, so a tick re-renders only the
  // timestamps whose label actually changed.
  const label = useSyncExternalStore(subscribe, () => relativeTime(iso, Date.now()));
  return (
    <time
      dateTime={iso}
      title={absoluteTime(iso)}
      className="whitespace-nowrap text-muted-foreground"
    >
      {label}
    </time>
  );
}
