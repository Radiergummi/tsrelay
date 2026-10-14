import { Link } from "react-router";
import { useHooks } from "@/api/queries";
import type { Hook } from "@/api/schemas";
import { EmptyState, PageError, TableSkeleton } from "@/components/PageState";
import { buttonVariants } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { LastCall } from "@/features/hooks/LastCall";
import { TunnelText } from "@/features/hooks/TunnelText";
import { absoluteTime } from "@/lib/format";
import { useViewer } from "@/lib/useViewer";

export function HooksPage() {
  const me = useViewer();
  const hooks = useHooks(false);
  return (
    <section>
      <div className="mb-4 flex flex-wrap items-baseline justify-between gap-3">
        <h1 className="text-lg font-medium">Hooks</h1>
        <div className="flex items-center gap-4 text-muted-foreground">
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
      ) : hooks.data.length === 0 ? (
        <EmptyState title="You have no hooks yet">
          Run <code>tsrelay http 8080</code> on your machine to get a hook whose calls reach port
          8080, or{" "}
          <Link to="/hooks/new" className="text-primary hover:underline">
            create one here
          </Link>
          .
        </EmptyState>
      ) : (
        <HooksTable hooks={hooks.data} />
      )}
    </section>
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
                  <div className="max-w-64 truncate text-muted-foreground">{h.description}</div>
                )}
              </TableCell>
              <TableCell>{h.channel}</TableCell>
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
