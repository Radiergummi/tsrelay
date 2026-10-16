import { Input } from "@/components/ui/input";
import { humanSpan, parseDuration } from "@/lib/duration";
import { fromLocalInput, localInputValue, rfc3339 } from "@/lib/format";
import type { FieldControl } from "./Field";

const hour = 3_600_000;
const day = 24 * hour;
const picks: [string, number][] = [
  ["1 h", hour],
  ["1 d", day],
  ["7 d", 7 * day],
  ["30 d", 30 * day],
];

/**
 * ExpiryField picks when a hook expires, in the browser's time zone; value
 * is RFC 3339 in UTC, or "" for never. A persistent hook under a grant with
 * maxHookTTL must expire within it, so longer quick picks and "never" go.
 */
export function ExpiryField({
  control,
  value,
  onChange,
  maxTTL,
}: {
  control: FieldControl;
  value: string;
  onChange: (value: string) => void;
  maxTTL: string | undefined;
}) {
  const maxMs = maxTTL ? parseDuration(maxTTL) : null;
  // oxlint-disable-next-line react/purity -- a coarse span, recomputed on every change, needs no ticking
  const left = value ? Date.parse(value) - Date.now() : null;
  const readout = [
    left === null ? "never expires" : left > 0 ? `in ${humanSpan(left)}` : "in the past",
    maxMs !== null && `your grants allow at most ${humanSpan(maxMs)}`,
  ]
    .filter(Boolean)
    .join(" · ");
  const chip =
    "inline-flex h-7 items-center rounded-full border border-input px-2.5 text-sm outline-none hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50";
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <Input
          type="datetime-local"
          className="w-auto"
          id={control.id}
          aria-invalid={control["aria-invalid"]}
          aria-describedby={control["aria-describedby"]}
          value={value ? localInputValue(value) : ""}
          onChange={(e) => onChange(fromLocalInput(e.target.value))}
        />
        {picks
          .filter(([, ms]) => maxMs === null || ms <= maxMs)
          .map(([label, ms]) => (
            <button
              key={label}
              type="button"
              className={chip}
              aria-label={`Expire in ${label}`}
              onClick={() => onChange(fromNow(ms))}
            >
              {label}
            </button>
          ))}
        {maxMs === null && (
          <button
            type="button"
            className={chip}
            aria-label="Never expire"
            onClick={() => onChange("")}
          >
            never
          </button>
        )}
      </div>
      <p className="text-xs text-muted-foreground" aria-live="polite">
        {readout}
      </p>
    </div>
  );
}

/** fromNow is ms from now, to the minute, rounded down so a quick pick stays within maxHookTTL. */
const fromNow = (ms: number) => rfc3339(new Date(Math.floor((Date.now() + ms) / 60_000) * 60_000));
