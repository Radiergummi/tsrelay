import { Link, useParams, useSearchParams } from "react-router";
import { paths } from "@/api/client";
import { useChannel } from "@/api/queries";
import type { Hook } from "@/api/schemas";
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
import { TunnelText } from "@/features/hooks/TunnelText";
import { Reveal } from "@/features/inspector/Reveal";
import { useAllEvents } from "@/lib/live";
import { useViewer } from "@/lib/useViewer";
import { isAdmin } from "@/lib/viewer";

/** ChannelPage shows a channel's hooks (every user's for an admin's ?all=true) and what the viewer may do on it. */
export function ChannelPage() {
  const me = useViewer();
  const { name = "" } = useParams();
  const [search] = useSearchParams();
  const all = isAdmin(me) && search.get("all") === "true";
  useAllEvents(all);
  const channel = useChannel(name, all);
  const here = paths.channel(name);
  return (
    <section>
      <div className="mb-4 flex flex-wrap items-baseline justify-between gap-3">
        <h1 className="text-lg font-medium break-all">
          <span className="text-muted-foreground">Channel</span> {name}
        </h1>
        {isAdmin(me) && (
          <Link to={all ? here : `${here}?all=true`} className="text-primary hover:underline">
            {all ? "Only your hooks" : "Every user's hooks"}
          </Link>
        )}
      </div>
      <QueryView query={channel}>
        {({ canReceive, canSend, hooks }) => (
          <>
            <p className="mb-6 text-muted-foreground">
              You {canReceive ? "may" : "may not"} receive on this channel, and{" "}
              {canSend ? "may" : "may not"} send on it. Who else may send on it is up to the tailnet
              policy.
            </p>
            {hooks.length === 0 ? (
              <EmptyState title={all ? "No hooks use this channel" : "You have no hooks here"} />
            ) : (
              <ChannelHooks hooks={hooks} all={all} />
            )}
          </>
        )}
      </QueryView>
    </section>
  );
}

function ChannelHooks({ hooks, all }: { hooks: Hook[]; all: boolean }) {
  return (
    <div className="overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow>
            {all && <TableHead>Owner</TableHead>}
            <TableHead>Path</TableHead>
            <TableHead>Kind</TableHead>
            <TableHead>Tunnel</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {hooks.map((hook) => (
            <TableRow key={hook.id}>
              {all && <TableCell>{hook.owner}</TableCell>}
              <TableCell>
                <Link to={paths.hook(hook.id)} className="font-medium hover:underline">
                  <Reveal text={hook.path} />
                </Link>
              </TableCell>
              <TableCell>{hook.ephemeral ? "Ephemeral" : "Persistent"}</TableCell>
              <TableCell>
                <TunnelText tunnel={hook.tunnel} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
