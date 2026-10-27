import { Link, useSearchParams } from "react-router";
import { paths } from "@/api/client";
import { useChannels, useHooks } from "@/api/queries";
import type { Hook } from "@/api/schemas";
import { EmptyState } from "@/components/PageState";
import { QueryView } from "@/components/QueryView";
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
            onChange={(picked) => setSearch(picked ? { channel: picked } : {}, { replace: true })}
          />
          <span>
            {me.hookCount} of {me.limits.maxHooks}
          </span>
          <Link to="/hooks/new" className={buttonVariants()}>
            New hook
          </Link>
        </div>
      </div>
      <QueryView query={hooks}>
        {(list) =>
          list.length === 0 && channel ? (
            <EmptyState title={`You have no hooks in ${channel}`} />
          ) : list.length === 0 ? (
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
            <HooksTable hooks={list} />
          )
        }
      </QueryView>
    </section>
  );
}

/** ChannelFilter picks one of the channels the viewer's hooks are in, or all of them. */
function ChannelFilter({
  value,
  onChange,
}: {
  value: string;
  onChange: (channel: string) => void;
}) {
  const channels = useChannels(false);
  const names = channels.data?.channels.map(({ name }) => name) ?? [];
  if (value && !names.includes(value)) names.unshift(value);
  return (
    <NativeSelect
      aria-label="Channel"
      className="w-auto"
      value={value}
      onChange={(event) => onChange(event.target.value)}
    >
      <option value="">All channels</option>
      {names.map((name) => (
        <option key={name} value={name}>
          {name}
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
          {hooks.map((hook) => (
            <TableRow key={hook.id}>
              <TableCell>
                <Link to={paths.hook(hook.id)} className="font-medium hover:underline">
                  {hook.path}
                </Link>
                {hook.description && (
                  <div className="max-w-64 truncate text-muted-foreground">
                    <Reveal text={hook.description} />
                  </div>
                )}
              </TableCell>
              <TableCell>
                <ChannelChip name={hook.channel} />
              </TableCell>
              <TableCell>
                {hook.ephemeral ? "Ephemeral" : "Persistent"}
                {hook.public && " · Public"}
              </TableCell>
              <TableCell>
                <TunnelText tunnel={hook.tunnel} />
              </TableCell>
              <TableCell>
                <LastCall call={hook.lastCall} />
              </TableCell>
              <TableCell className="whitespace-nowrap">
                {hook.expiresAt ? absoluteTime(hook.expiresAt) : "Never"}
              </TableCell>
              <TableCell>
                <span className="flex items-center gap-1">
                  <code className="max-w-72 truncate text-xs">{hook.url}</code>
                  <CopyButton text={hook.url} label="hook URL" />
                </span>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
