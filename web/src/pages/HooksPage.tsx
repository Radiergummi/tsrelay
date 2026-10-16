import { Link, useSearchParams } from "react-router";
import { useChannels, useHooks } from "@/api/queries";
import type { Hook } from "@/api/schemas";
import { EmptyState, PageError, TableSkeleton } from "@/components/PageState";
import { buttonVariants } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { NativeSelect } from "@/components/ui/native-select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ChannelChip } from "@/features/channels/ChannelChip";
import { InstallCommand } from "@/features/cli/InstallCommand";
import { LastCall } from "@/features/hooks/LastCall";
import { TunnelText } from "@/features/hooks/TunnelText";
import { Reveal } from "@/features/inspector/Reveal";
import { absoluteTime } from "@/lib/format";
import { detectPlatform } from "@/lib/install";
import { useViewer } from "@/lib/useViewer";

export function HooksPage() {
  const me = useViewer();
  const [search, setSearch] = useSearchParams();
  const channel = search.get("channel") ?? "";
  const hooks = useHooks(false, channel);
  return (
    <section>
      <div className="mb-4 flex flex-wrap items-baseline justify-between gap-3">
        <h1 className="text-lg font-medium">Hooks</h1>
        <div className="flex items-center gap-4 text-muted-foreground">
          <ChannelFilter
            value={channel}
            onChange={(c) => setSearch(c ? { channel: c } : {}, { replace: true })}
          />
          <span>
            {me.hooks} of {me.limits.maxHooks}
          </span>
          <Link to="/hooks/new" className={buttonVariants()}>
            New hook
          </Link>
        </div>
      </div>
      {hooks.isPending ? (
        <TableSkeleton />
      ) : hooks.isError ? (
        <PageError error={hooks.error} />
      ) : hooks.data.length === 0 && channel ? (
        <EmptyState title={`You have no hooks in ${channel}`} />
      ) : hooks.data.length === 0 ? (
        <EmptyState title="You have no hooks yet">
          <div className="mx-auto max-w-xl space-y-3">
            <p>Install the CLI:</p>
            <InstallCommand platform={detectPlatform()} />
            <p>
              Then run <code>tsrelay http 8080</code> on your machine to get a hook whose calls
              reach port 8080, or{" "}
              <Link to="/hooks/new" className="text-primary hover:underline">
                create one here
              </Link>
              .{" "}
              <Link to="/cli" className="text-primary hover:underline">
                Other ways to install
              </Link>
            </p>
          </div>
        </EmptyState>
      ) : (
        <HooksTable hooks={hooks.data} />
      )}
    </section>
  );
}

/** ChannelFilter picks one of the channels the viewer's hooks are in, or all of them. */
function ChannelFilter({ value, onChange }: { value: string; onChange: (c: string) => void }) {
  const channels = useChannels(false);
  const names = channels.data?.channels.map((c) => c.name) ?? [];
  if (value && !names.includes(value)) names.unshift(value);
  return (
    <NativeSelect
      aria-label="Channel"
      className="w-auto"
      value={value}
      onChange={(e) => onChange(e.target.value)}
    >
      <option value="">All channels</option>
      {names.map((n) => (
        <option key={n} value={n}>
          {n}
        </option>
      ))}
    </NativeSelect>
  );
}

function HooksTable({ hooks }: { hooks: Hook[] }) {
  return (
    <div className="overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Path</TableHead>
            <TableHead>Channel</TableHead>
            <TableHead>Kind</TableHead>
            <TableHead>Tunnel</TableHead>
            <TableHead>Last call</TableHead>
            <TableHead>Expires</TableHead>
            <TableHead>URL</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {hooks.map((h) => (
            <TableRow key={h.id}>
              <TableCell>
                <Link
                  to={`/hooks/${encodeURIComponent(h.id)}`}
                  className="font-medium hover:underline"
                >
                  {h.path}
                </Link>
                {h.description && (
                  <div className="max-w-64 truncate text-muted-foreground">
                    <Reveal text={h.description} />
                  </div>
                )}
              </TableCell>
              <TableCell>
                <ChannelChip name={h.channel} />
              </TableCell>
              <TableCell>{h.ephemeral ? "Ephemeral" : "Persistent"}</TableCell>
              <TableCell>
                <TunnelText tunnel={h.tunnel} />
              </TableCell>
              <TableCell>
                <LastCall call={h.lastCall} />
              </TableCell>
              <TableCell className="whitespace-nowrap">
                {h.expiresAt ? absoluteTime(h.expiresAt) : "Never"}
              </TableCell>
              <TableCell>
                <span className="flex items-center gap-1">
                  <code className="max-w-72 truncate text-xs">{h.url}</code>
                  <CopyButton text={h.url} label="hook URL" />
                </span>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
