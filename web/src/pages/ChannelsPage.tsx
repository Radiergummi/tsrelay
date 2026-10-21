import { Link, useSearchParams } from "react-router";
import { useChannels } from "@/api/queries";
import type { Channels } from "@/api/schemas";
import { EmptyState } from "@/components/PageState";
import { QueryView } from "@/components/QueryView";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ChannelChip } from "@/features/channels/ChannelChip";
import { Reveal } from "@/features/inspector/Reveal";
import { useAllEvents } from "@/lib/live";
import { useViewer } from "@/lib/useViewer";
import { isAdmin } from "@/lib/viewer";

/**
 * ChannelsPage shows the viewer's channel patterns and the channels their
 * hooks are in; admins can switch to every channel in use (?all=true).
 * Who else may send on a channel is in the tailnet policy, which the relay
 * doesn't see (it learns each caller's grants per request).
 */
export function ChannelsPage() {
  const me = useViewer();
  const [search] = useSearchParams();
  const all = isAdmin(me) && search.get("all") === "true";
  useAllEvents(all);
  const channels = useChannels(all);
  return (
    <section>
      <div className="mb-4 flex flex-wrap items-baseline justify-between gap-3">
        <h1 className="text-lg font-medium">Channels</h1>
        {isAdmin(me) && (
          <Link
            to={all ? "/channels" : "/channels?all=true"}
            className="text-primary hover:underline"
          >
            {all ? "Only your hooks' channels" : "Every user's channels"}
          </Link>
        )}
      </div>
      <QueryView query={channels}>
        {(c) => (
          <>
            <Patterns channels={c} />
            {c.channels.length === 0 ? (
              <EmptyState title={all ? "No hooks use a channel yet" : "You have no hooks yet"} />
            ) : (
              <ChannelsTable channels={c} all={all} />
            )}
          </>
        )}
      </QueryView>
    </section>
  );
}

function Patterns({ channels }: { channels: Channels }) {
  const list = (patterns: string[]) =>
    patterns.length ? (
      patterns.map((p) => (
        <code key={p} className="mr-2 text-xs">
          {p}
        </code>
      ))
    ) : (
      <span className="text-muted-foreground">none</span>
    );
  return (
    <dl className="mb-6 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
      <dt className="text-muted-foreground">You receive on</dt>
      <dd>{list(channels.receive)}</dd>
      <dt className="text-muted-foreground">You send on</dt>
      <dd>{list(channels.send)}</dd>
    </dl>
  );
}

function ChannelsTable({ channels, all }: { channels: Channels; all: boolean }) {
  return (
    <div className="overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Channel</TableHead>
            <TableHead>Hooks</TableHead>
            {!all && <TableHead>Received through</TableHead>}
          </TableRow>
        </TableHeader>
        <TableBody>
          {channels.channels.map((c) => (
            <TableRow key={c.name}>
              <TableCell>
                <ChannelChip name={c.name} />
              </TableCell>
              <TableCell>
                {c.hooks ? (
                  <span className="flex flex-wrap gap-x-3">
                    {c.hooks.map((h) => (
                      <Link
                        key={h.id}
                        to={`/hooks/${encodeURIComponent(h.id)}`}
                        className="hover:underline"
                      >
                        <Reveal text={h.path} />
                      </Link>
                    ))}
                  </span>
                ) : (
                  c.hookCount
                )}
              </TableCell>
              {!all && (
                <TableCell>
                  {c.receive.length ? (
                    <code className="text-xs">{c.receive.join(", ")}</code>
                  ) : (
                    <span className="text-muted-foreground">no longer granted</span>
                  )}
                </TableCell>
              )}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
