import { useQueryClient } from "@tanstack/react-query";
import { Navigate, Outlet, useLocation } from "react-router";
import { ApiError } from "@/api/client";
import { useMe } from "@/api/queries";
import { Button } from "@/components/ui/button";
import { LiveProvider } from "@/lib/live";
import { isAdmin, isReceiver, isUser } from "@/lib/viewer";
import { NoAccessPage } from "@/pages/NoAccessPage";
import { Header } from "./Header";
import { LiveBar } from "./LiveBar";
import { TableSkeleton } from "./PageState";

/**
 * Layout loads /me before any page: who the viewer is decides the header,
 * whether there is anything to show (no access otherwise), and where an
 * admin without the receiver role starts.
 */
export function Layout() {
  const me = useMe();
  const client = useQueryClient();
  const location = useLocation();
  if (me.isPending) {
    return (
      <main className="mx-auto max-w-[1400px] px-6 py-6">
        <TableSkeleton />
      </main>
    );
  }
  if (me.isError) return <NotIdentified error={me.error} />;
  const viewer = me.data;
  const allowed = isReceiver(viewer) || isAdmin(viewer);
  if (allowed && !isReceiver(viewer) && location.pathname === "/")
    return <Navigate to="/admin" replace />;
  return (
    <LiveProvider client={client} user={isUser(viewer)}>
      <Header me={viewer} />
      <LiveBar />
      <main className="mx-auto w-full max-w-[1400px] px-4 py-6 sm:px-6">
        {allowed ? <Outlet context={viewer} /> : <NoAccessPage me={viewer} />}
      </main>
    </LiveProvider>
  );
}

function NotIdentified({ error }: { error: unknown }) {
  const unidentified = error instanceof ApiError && error.status === 403;
  return (
    <main className="mx-auto max-w-xl px-6 py-16">
      <h1 className="mb-2 text-lg font-medium">
        {unidentified ? "The relay doesn't know who you are" : "The relay is not reachable"}
      </h1>
      <p className="mb-4 text-muted-foreground">
        {unidentified
          ? "The relay could not identify your device on the tailnet. Reload the page; if this keeps happening, the relay may have lost its Tailscale connection."
          : error instanceof Error
            ? error.message
            : String(error)}
      </p>
      <Button onClick={() => window.location.reload()}>Reload</Button>
    </main>
  );
}
