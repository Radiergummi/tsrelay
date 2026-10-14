import type { Summary } from "@/api/schemas";
import { StatusText } from "@/components/StatusText";
import { TimeAgo } from "@/components/TimeAgo";

export function LastCall({
  call,
  withRequest = true,
}: {
  call: Summary | undefined;
  withRequest?: boolean;
}) {
  if (!call) return <span className="text-muted-foreground">none yet</span>;
  return (
    <span className="flex items-baseline gap-2 whitespace-nowrap">
      <StatusText call={call} short />
      {withRequest && (
        <code className="max-w-56 truncate text-xs">
          {call.method} {call.path}
        </code>
      )}
      <TimeAgo iso={call.receivedAt} />
    </span>
  );
}
