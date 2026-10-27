import { useQueryClient } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Navigate, Outlet, useLocation } from "react-router";
import { ApiError } from "@/api/client";
import { useMe } from "@/api/queries";
import type { Me } from "@/api/schemas";
import { LiveProvider } from "@/lib/live";
import { isAdmin, isReceiver, mayStream } from "@/lib/viewer";
import { NoAccessPage } from "@/pages/NoAccessPage";
import { Footer } from "./Footer";
import { Header } from "./Header";
import { LiveBar } from "./LiveBar";
import { errorText, TableSkeleton } from "./PageState";
import { QueryView } from "./QueryView";
import { Button } from "./ui/button";

/**
 * Layout loads the entry document (GET /) before any page: who the viewer
 * is decides the header, whether there is anything to show (no access
 * otherwise), and where an
 * admin without the receiver role starts.
 */
export function Layout() {
  const me = useMe();
  // A failed refetch keeps the app on the last entry document.
  return (
    <QueryView
      query={me}
      pending={
        <Page>
          <main className="mx-auto w-full max-w-page px-6 py-6">
            <TableSkeleton />
          </main>
        </Page>
      }
      error={NotIdentified}
    >
      {(viewer) => <Viewer viewer={viewer} />}
    </QueryView>
  );
}

/** Viewer is the app for an identified viewer: their header, live events and the page. */
function Viewer({ viewer }: { viewer: Me }) {
  const client = useQueryClient();
  const location = useLocation();
  const allowed = isReceiver(viewer) || isAdmin(viewer);
  if (allowed && !isReceiver(viewer) && location.pathname === "/hooks")
    return <Navigate to="/admin" replace />;
  // The licenses page needs no role, so the footer's link works for everyone.
  const open = allowed || location.pathname === "/licenses";
  return (
    <LiveProvider client={client} user={mayStream(viewer)}>
      <Page me={viewer}>
        <Header me={viewer} />
        <LiveBar />
        <main className="mx-auto w-full max-w-page px-4 py-6 sm:px-6">
          {open ? <Outlet context={viewer} /> : <NoAccessPage me={viewer} />}
        </main>
      </Page>
    </LiveProvider>
  );
}

/** Page puts the footer under every state of the layout. */
function Page({ me, children }: { me?: Me; children: ReactNode }) {
  return (
    <div className="flex min-h-screen flex-col">
      {children}
      <Footer version={me?.version} commit={me?.commit} />
    </div>
  );
}

function NotIdentified({ error }: { error: unknown }) {
  const unidentified = error instanceof ApiError && error.code === "not_identified";
  return (
    <Page>
      <main className="mx-auto max-w-xl px-6 py-16">
        <h1 className="mb-2 text-lg font-medium">
          {unidentified ? "The relay doesn't know who you are" : "The relay is not reachable"}
        </h1>
        <p className="mb-4 text-muted-foreground">
          {unidentified
            ? "The relay could not identify your device on the tailnet. Reload the page; if this keeps happening, the relay may have lost its Tailscale connection."
            : errorText(error)}
        </p>
        <Button onClick={() => window.location.reload()}>Reload</Button>
      </main>
    </Page>
  );
}
