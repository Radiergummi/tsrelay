import type { Attempt } from "@/api/schemas";
import { StatusText } from "@/components/StatusText";
import { parseDuration, formatDuration } from "@/lib/duration";
import { absoluteTime, formatSize } from "@/lib/format";

const dur = (go: string) => formatDuration(parseDuration(go) ?? 0);

/** Attempts is the timeline of a call's deliveries: one per tunnel it was sent to. */
export function Attempts({ attempts }: { attempts: Attempt[] }) {
  if (attempts.length === 0)
    return (
      <p className="text-muted-foreground">No attempts. The call was refused before delivery.</p>
    );
  return (
    <ol className="space-y-3">
      {attempts.map((a, i) => (
        <li key={a.id} className="border-l-2 border-border pl-3">
          <div className="flex flex-wrap items-baseline gap-x-3">
            <span className="font-medium">Attempt {i + 1}</span>
            {a.status ? (
              <StatusText call={{ outcome: "ok", status: a.status }} />
            ) : (
              <span className="text-status-5xx">{a.error ?? "no response"}</span>
            )}
            <span className="text-muted-foreground">{absoluteTime(a.startedAt)}</span>
          </div>
          <dl className="mt-1 grid grid-cols-[auto_1fr] gap-x-4 text-xs">
            <dt className="text-muted-foreground">Node</dt>
            <dd>{a.node ?? "—"}</dd>
            <dt className="text-muted-foreground">Tunnel</dt>
            <dd className="font-mono">{a.tunnelId ?? "—"}</dd>
            <dt className="text-muted-foreground">Waited for a tunnel</dt>
            <dd>{dur(a.waitedFor)}</dd>
            <dt className="text-muted-foreground">Time to first byte</dt>
            <dd>{dur(a.ttfb)}</dd>
            <dt className="text-muted-foreground">Duration</dt>
            <dd>{dur(a.duration)}</dd>
            <dt className="text-muted-foreground">Bytes in / out</dt>
            <dd>
              {formatSize(a.bytesIn)} / {formatSize(a.bytesOut)}
            </dd>
            {a.error && (
              <>
                <dt className="text-muted-foreground">Error</dt>
                <dd className="font-mono">{a.error}</dd>
              </>
            )}
          </dl>
        </li>
      ))}
    </ol>
  );
}
