import { useQueryClient } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Navigate, Outlet, useLocation } from "react-router";
import { ApiError } from "@/api/client";
import { useMe } from "@/api/queries";
import type { Me } from "@/api/schemas";
import { LiveProvider } from "@/lib/live";
import { isAdmin, isReceiver, isUser } from "@/lib/viewer";
import { NoAccessPage } from "@/pages/NoAccessPage";
import { Footer } from "./Footer";
import { Header } from "./Header";
import { LiveBar } from "./LiveBar";
import { QueryView } from "./QueryView";

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
          <main className="mx-auto w-full max-w-[1400px] px-6 py-6">
            <LoadingSkeleton />
          </main>
        </Page>
      }
      error={(e) => (
        <Page>
          <NotIdentified error={e} />
        </Page>
      )}
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
    <LiveProvider client={client} user={isUser(viewer)}>
      <Page me={viewer}>
        <Header me={viewer} />
        <LiveBar />
        <main className="mx-auto w-full max-w-[1400px] px-4 py-6 sm:px-6">
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

// Not the shared <TableSkeleton/> (components/PageState.tsx): that goes
// through <Skeleton/>, which uses cn()/tailwind-merge, and pulling that into
// the layout's own eager bundle blows the 120 KiB budget for one loading
// flash. This renders the exact same markup without it; PageState's
// TableSkeleton is untouched for the lazy-loaded pages that use it.
//
// Kept in sync with <Skeleton className="h-8 w-full" /> (ui/skeleton.tsx)
// by Layout.classes.test.tsx, which renders a real Skeleton and compares.
export const loadingRowClassName = "h-8 w-full animate-pulse rounded-md bg-muted";

function LoadingSkeleton() {
  return (
    <div className="space-y-2" aria-busy="true" aria-label="Loading">
      {Array.from({ length: 5 }, (_, i) => (
        <div key={i} className={loadingRowClassName} />
      ))}
    </div>
  );
}

// These are the classes buttonVariants({variant: "default", size: "default"})
// (ui/button.tsx) produces, minus the `[&_svg]...` icon-child selectors,
// which don't apply to this icon-less button. Kept in sync by
// Layout.classes.test.tsx, which calls the real buttonVariants() and checks
// every one of its (non-icon) classes is present here.
export const reloadButtonClassName =
  "group/button inline-flex h-8 shrink-0 items-center justify-center gap-1.5 rounded-md border border-transparent bg-primary bg-clip-padding px-2.5 text-sm font-medium whitespace-nowrap text-primary-foreground transition-all outline-none select-none hover:bg-primary/90 focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-50 has-data-[icon=inline-end]:pr-2 has-data-[icon=inline-start]:pl-2 aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 dark:aria-invalid:border-destructive/50 dark:aria-invalid:ring-destructive/40";

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
      {/*
        Not the shared <Button/>: it goes through cn()/tailwind-merge via
        buttonVariants(), and nothing else in the initial bundle needs that
        library once the theme toggle is lazy too.
      */}
      <button
        type="button"
        onClick={() => window.location.reload()}
        className={reloadButtonClassName}
      >
        Reload
      </button>
    </main>
  );
}
