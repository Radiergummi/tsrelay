import { outcomeLabel, statusClass } from "@/lib/format";
import { relayPhrase } from "@/lib/httpStatus";
import { cn } from "@/lib/utils";

const colours = {
  "2xx": "text-status-2xx",
  "3xx": "text-status-3xx",
  "4xx": "text-status-4xx",
  "5xx": "text-status-5xx",
  failed: "text-status-5xx",
  pending: "text-status-pending",
  other: "text-muted-foreground",
};

/**
 * StatusText is a call's status ("200 OK", "563 Tunnel lost", "pending"), its
 * numeral coloured by class. A status of the relay's 56x range is set in bold,
 * marking it as the relay's rather than the app's.
 */
export function StatusText({
  call,
  short = false,
}: {
  call: { outcome: string; status?: number | undefined };
  short?: boolean;
}) {
  const label = short ? (call.status ? String(call.status) : call.outcome) : outcomeLabel(call);
  const relay = relayPhrase(call.status ?? 0);
  return (
    <span
      className={cn("font-mono tabular-nums", colours[statusClass(call)], relay && "font-semibold")}
      title={short && relay ? relay : undefined}
    >
      {label}
    </span>
  );
}
