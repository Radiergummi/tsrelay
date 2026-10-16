import type { ReactNode } from "react";
import type { Effective } from "@/api/schemas";
import { CopyButton } from "@/components/ui/copy-button";
import { humanDuration, humanSpan, parseDuration } from "@/lib/duration";
import { formatSize } from "@/lib/format";
import { cn } from "@/lib/utils";

/**
 * HookPreview sums up the hook the form would save: its URL, channel,
 * lifetime and every effective delivery setting, marking the defaults.
 */
export function HookPreview({
  url,
  channel,
  pattern,
  ephemeral,
  expiresAt,
  effective,
  inherited,
  className,
}: {
  url: string | undefined; // undefined: three random words, chosen on save
  channel: string;
  pattern: string | undefined;
  ephemeral: boolean;
  expiresAt: string;
  effective: Effective;
  inherited: (setting: keyof Effective) => boolean;
  className?: string;
}) {
  const dur = (go: string) => {
    const ms = parseDuration(go) ?? 0;
    return ms === 0 ? "off" : humanDuration(ms);
  };
  const rows: [string, keyof Effective, string][] = [
    ["Hold", "holdTimeout", dur(effective.holdTimeout)],
    ["Response", "responseTimeout", dur(effective.responseTimeout)],
    ["Idle", "idleTimeout", dur(effective.idleTimeout)],
    [
      "Body",
      "maxBodyBytes",
      effective.maxBodyBytes > 0 ? `≤ ${formatSize(effective.maxBodyBytes)}` : "unlimited",
    ],
    ["Rate", "rateLimit", `${effective.rateLimit.perSecond}/s, burst ${effective.rateLimit.burst}`],
    ["Attach", "attachPolicy", effective.attachPolicy === "reject" ? "reject" : "take over"],
    ["Host", "hostHeader", effective.hostHeader === "rewrite" ? "its own host" : "relay's host"],
    ["Capture", "capture", effective.capture ? "on" : "off"],
  ];
  return (
    <aside
      aria-labelledby="hook-preview"
      className={cn("space-y-3 rounded-lg border bg-card p-4 text-sm", className)}
    >
      <h2 id="hook-preview" className="font-medium">
        Preview
      </h2>
      {url ? (
        <div className="flex items-start gap-1">
          <code className="min-w-0 flex-1 text-xs break-all">{url}</code>
          <CopyButton text={url} label="hook URL" />
        </div>
      ) : (
        <p className="text-xs text-muted-foreground">
          The path is three random words, chosen on save.
        </p>
      )}
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1">
        <Row name="Channel">
          {channel}
          {pattern && (
            <span className="text-xs text-muted-foreground">
              {" "}
              via <code>{pattern}</code>
            </span>
          )}
        </Row>
        <Row name="Lifetime">{lifetime(ephemeral, expiresAt)}</Row>
        {rows.map(([name, key, value]) => (
          <Row key={key} name={name}>
            {value}
            {inherited(key) && <span className="text-xs text-muted-foreground"> default</span>}
          </Row>
        ))}
      </dl>
    </aside>
  );
}

function Row({ name, children }: { name: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{name}</dt>
      <dd>{children}</dd>
    </>
  );
}

/** lifetime: "persistent, until 31 Dec (76 d)", or "ephemeral, no expiry". */
function lifetime(ephemeral: boolean, expiresAt: string): string {
  const kind = ephemeral ? "ephemeral" : "persistent";
  if (!expiresAt) return `${kind}, no expiry`;
  const at = new Date(expiresAt);
  const sameYear = at.getFullYear() === new Date().getFullYear();
  const date = at.toLocaleDateString(undefined, {
    day: "numeric",
    month: "short",
    year: sameYear ? undefined : "numeric",
  });
  const left = at.getTime() - Date.now();
  return `${kind}, until ${date} (${left > 0 ? humanSpan(left) : "past"})`;
}
