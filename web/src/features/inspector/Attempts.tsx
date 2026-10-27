import type { Attempt } from "@/api/schemas";
import { StatusText } from "@/components/StatusText";
import { formatDuration } from "@/lib/duration";
import { absoluteTime, formatSize } from "@/lib/format";

/** Attempts is the timeline of a call's deliveries: one per tunnel it was sent to. */
export function Attempts({ attempts }: { attempts: Attempt[] }) {
  if (attempts.length === 0)
    return (
      <p className="text-muted-foreground">No attempts. The call was refused before delivery.</p>
    );
  return (
    <ol className="space-y-3">
      {attempts.map((attempt, i) => (
        <li key={attempt.id} className="border-l-2 border-border pl-3">
          <div className="flex flex-wrap items-baseline gap-x-3">
            <span className="font-medium">Attempt {i + 1}</span>
            {attempt.status ? (
              <StatusText call={{ outcome: "ok", status: attempt.status }} />
            ) : (
              <span className="text-status-5xx">{attempt.error ?? "no response"}</span>
            )}
            <span className="text-muted-foreground">{absoluteTime(attempt.startedAt)}</span>
          </div>
          <dl className="mt-1 grid grid-cols-[auto_1fr] gap-x-4 text-xs">
            <dt className="text-muted-foreground">Node</dt>
            <dd>{attempt.node ?? "—"}</dd>
            <dt className="text-muted-foreground">Tunnel</dt>
            <dd className="font-mono">{attempt.tunnelId ?? "—"}</dd>
            <dt className="text-muted-foreground">Waited for a tunnel</dt>
            <dd>{formatDuration(attempt.waitedForMs)}</dd>
            <dt className="text-muted-foreground">Time to first byte</dt>
            <dd>{formatDuration(attempt.ttfbMs)}</dd>
            <dt className="text-muted-foreground">Duration</dt>
            <dd>{formatDuration(attempt.durationMs)}</dd>
            <dt className="text-muted-foreground">Bytes in / out</dt>
            <dd>
              {formatSize(attempt.bytesIn)} / {formatSize(attempt.bytesOut)}
            </dd>
            {attempt.error && (
              <>
                <dt className="text-muted-foreground">Error</dt>
                <dd className="font-mono">{attempt.error}</dd>
              </>
            )}
          </dl>
        </li>
      ))}
    </ol>
  );
}
