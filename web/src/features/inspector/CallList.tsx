import { useEffect, useRef } from "react";
import { Link } from "react-router";
import type { Summary } from "@/api/schemas";
import { StatusText } from "@/components/StatusText";
import { TimeAgo } from "@/components/TimeAgo";
import { formatGoDuration } from "@/lib/duration";
import { senderLabel } from "@/lib/format";
import { cn } from "@/lib/utils";
import { Reveal } from "./Reveal";

/** CallList is the hook's calls, newest first; the selected one is marked and kept in view. */
export function CallList({
  calls,
  hookId,
  selected,
  search,
}: {
  calls: Summary[];
  hookId: string;
  selected: string | undefined;
  search: string;
}) {
  const selectedRef = useRef<HTMLAnchorElement>(null);
  // selected decides which element the ref attaches to, not something read
  // in the callback. scrollIntoView() can return a Promise (new browsers
  // resolve it once scrolling settles); void it, since an effect's return
  // value must be a cleanup function or undefined, never that promise.
  // oxlint-disable-next-line react/exhaustive-effect-dependencies
  useEffect(() => void selectedRef.current?.scrollIntoView({ block: "nearest" }), [selected]);
  if (calls.length === 0)
    return <p className="py-8 text-center text-muted-foreground">No calls match.</p>;
  return (
    <ul className="space-y-0.5">
      {calls.map((c) => {
        const on = c.id === selected;
        return (
          <li key={c.id}>
            <Link
              ref={on ? selectedRef : undefined}
              to={{
                pathname: `/hooks/${encodeURIComponent(hookId)}/calls/${encodeURIComponent(c.id)}`,
                search,
              }}
              aria-current={on ? "true" : undefined}
              className={cn(
                "grid grid-cols-[1fr_auto] gap-x-3 rounded-md px-2 py-1.5 hover:bg-muted/60",
                on && "bg-accent text-accent-foreground hover:bg-accent",
              )}
            >
              <span className="truncate font-mono text-xs">
                {c.method} <Reveal text={c.path} />
                {c.kind !== "sender" && (
                  <span className="ml-1.5 rounded-sm bg-muted px-1 text-[0.7rem] text-muted-foreground">
                    {c.kind}
                  </span>
                )}
              </span>
              <span className="text-right">
                <StatusText call={c} short />
              </span>
              <span className="truncate text-xs text-muted-foreground">
                {senderLabel(c.sender)}
              </span>
              <span className="text-xs text-muted-foreground">
                {c.outcome !== "pending" && `${formatGoDuration(c.duration)} · `}
                <TimeAgo iso={c.receivedAt} />
              </span>
            </Link>
          </li>
        );
      })}
    </ul>
  );
}
