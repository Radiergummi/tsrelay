import { Trash2 } from "lucide-react";
import { Link } from "react-router";
import { paths } from "@/api/client";
import { useHooks } from "@/api/queries";
import type { Hook } from "@/api/schemas";
import { EmptyState } from "@/components/PageState";
import { QueryView } from "@/components/QueryView";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ChannelChip } from "@/features/channels/ChannelChip";
import { DeleteHookDialog } from "@/features/hooks/DeleteHookDialog";
import { LastCall } from "@/features/hooks/LastCall";
import { TunnelText } from "@/features/hooks/TunnelText";
import { Reveal } from "@/features/inspector/Reveal";
import { useAllEvents } from "@/lib/live";
import { useViewer } from "@/lib/useViewer";
import { isAdmin } from "@/lib/viewer";

/**
 * AdminPage lists every hook as metadata (DESIGN.md §5.2): owners, paths,
 * tunnels and the last call's status. Bodies, headers and queries never
 * appear, because the API doesn't send them to admins.
 */
export function AdminPage() {
  const me = useViewer();
  const admin = isAdmin(me);
  useAllEvents(admin);
  const hooks = useHooks(true);
  if (!admin) return <EmptyState title="Only relay admins can see this page." />;
  return <QueryView query={hooks}>{(data) => <AllHooks hooks={data} />}</QueryView>;
}

function AllHooks({ hooks }: { hooks: Hook[] }) {
  const list = [...hooks].sort(
    (a, b) => a.owner.localeCompare(b.owner) || a.path.localeCompare(b.path),
  );
  const tunnels = new Set(list.flatMap((h) => (h.tunnel ? [h.tunnel.id] : []))).size;
  return (
    <section>
      <div className="mb-4 flex items-baseline justify-between">
        <h1 className="text-lg font-medium">All hooks</h1>
        <span className="text-muted-foreground">
          {list.length} {list.length === 1 ? "hook" : "hooks"}, {tunnels}{" "}
          {tunnels === 1 ? "tunnel" : "tunnels"}
        </span>
      </div>
      <div className="overflow-x-auto">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Owner</TableHead>
              <TableHead>Path</TableHead>
              <TableHead>Channel</TableHead>
              <TableHead>Tunnel</TableHead>
              <TableHead>Last call</TableHead>
              <TableHead className="w-10" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {list.map((h) => (
              <TableRow key={h.id}>
                <TableCell>{h.owner}</TableCell>
                <TableCell>
                  <Link to={paths.hook(h.id)} className="font-medium hover:underline">
                    <Reveal text={h.path} />
                  </Link>
                </TableCell>
                <TableCell>
                  <ChannelChip name={h.channel} all />
                </TableCell>
                <TableCell>
                  <TunnelText tunnel={h.tunnel} />
                </TableCell>
                <TableCell>
                  <LastCall call={h.lastCall} withRequest={false} />
                </TableCell>
                <TableCell>
                  <DeleteHookDialog
                    hook={h}
                    trigger={
                      <Button variant="ghost" size="icon-sm" aria-label={`Delete ${h.path}`}>
                        <Trash2 />
                      </Button>
                    }
                  />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </section>
  );
}
