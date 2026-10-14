import { useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { ApiError } from "@/api/client";
import { useCalls, useHook } from "@/api/queries";
import type { Hook, Me } from "@/api/schemas";
import { EmptyState, PageError, TableSkeleton } from "@/components/PageState";
import { ShortcutsDialog } from "@/components/ShortcutsDialog";
import { HookHeader } from "@/features/hooks/HookHeader";
import { SettingsTable } from "@/features/hooks/SettingsTable";
import { TestRequestDialog } from "@/features/hooks/TestRequestDialog";
import { CallList } from "@/features/inspector/CallList";
import { FilterBar } from "@/features/inspector/FilterBar";
import { matches, readFilters, writeFilters } from "@/features/inspector/filters";
import { Inspector } from "@/features/inspector/Inspector";
import { useShortcuts } from "@/features/inspector/shortcuts";
import { useAllEvents } from "@/lib/live";
import { useViewer } from "@/lib/useViewer";
import { cn } from "@/lib/utils";
import { owns } from "@/lib/viewer";

/**
 * HookPage is the hook with its calls on the left and the inspector on the
 * right. Selecting a call changes the URL (the child route) without
 * remounting the page. Below 900px the panes stack, and a selected call
 * gets the whole width with a link back.
 */
export function HookPage() {
  const { id = "", callId } = useParams();
  const me = useViewer();
  const hook = useHook(id);
  const owner = hook.data ? owns(me, hook.data) : true;
  useAllEvents(hook.data !== undefined && !owner);
  if (hook.isPending) return <TableSkeleton />;
  if (hook.isError) {
    if (hook.error instanceof ApiError && hook.error.status === 404)
      return <EmptyState title="There is no hook with this ID, or it is not yours." />;
    return <PageError error={hook.error} />;
  }
  return <Loaded hook={hook.data} me={me} owner={owner} callId={callId} />;
}

function Loaded({
  hook,
  me,
  owner,
  callId,
}: {
  hook: Hook;
  me: Me;
  owner: boolean;
  callId: string | undefined;
}) {
  const hookId = hook.id;
  const calls = useCalls(hookId);
  const [params, setParams] = useSearchParams();
  const query = params.toString();
  const filters = useMemo(() => readFilters(new URLSearchParams(query)), [query]);
  const visible = useMemo(
    () => (calls.data ?? []).filter((c) => matches(c, filters)),
    [calls.data, filters],
  );
  const navigate = useNavigate();
  const search = useRef<HTMLInputElement>(null);
  const [testing, setTesting] = useState(false);
  const [help, setHelp] = useState(false);
  const base = `/hooks/${encodeURIComponent(hookId)}`;
  const qs = query ? `?${query}` : "";
  const select = (id: string | undefined, replace = true) =>
    navigate((id ? `${base}/calls/${encodeURIComponent(id)}` : base) + qs, { replace });
  const at = visible.findIndex((c) => c.id === callId);

  useShortcuts({
    j: () => {
      if (visible.length) select(visible[Math.min(at + 1, visible.length - 1)]!.id);
    },
    k: () => {
      if (visible.length) select(visible[Math.max(at - 1, 0)]!.id);
    },
    Enter: () => {
      if (!callId && visible[0]) select(visible[0].id);
    },
    Escape: () => {
      if (callId) select(undefined);
    },
    "/": () => search.current?.focus(),
    t: () => {
      if (owner) setTesting(true);
    },
    "?": () => setHelp(true),
  });

  return (
    <div>
      <HookHeader hook={hook} owner={owner} onTest={() => setTesting(true)} />
      <div className="grid gap-6 min-[900px]:grid-cols-[minmax(300px,400px)_1fr]">
        <section className={cn("min-w-0", callId && "hidden min-[900px]:block")} aria-label="Calls">
          <FilterBar
            calls={calls.data ?? []}
            filters={filters}
            onChange={(f) => setParams(writeFilters(f, params), { replace: true })}
            searchRef={search}
          />
          {calls.isPending ? (
            <TableSkeleton />
          ) : calls.isError ? (
            <PageError error={calls.error} />
          ) : calls.data.length === 0 ? (
            <p className="py-8 text-center text-muted-foreground">
              No calls yet. They appear here as they arrive.
            </p>
          ) : (
            <div className="max-h-[calc(100vh-16rem)] overflow-y-auto">
              <CallList calls={visible} hookId={hookId} selected={callId} search={qs} />
            </div>
          )}
        </section>
        <section
          className={cn("min-w-0", !callId && "hidden min-[900px]:block")}
          aria-label="Inspector"
        >
          {callId ? (
            <>
              <Link
                to={base + qs}
                className="mb-3 inline-block text-primary hover:underline min-[900px]:hidden"
              >
                ← Back to calls
              </Link>
              <Inspector hook={hook} me={me} callId={callId} onSelect={(id) => select(id, false)} />
            </>
          ) : (
            <SettingsTable hook={hook} />
          )}
        </section>
      </div>
      {owner && <TestRequestDialog hook={hook} open={testing} onOpenChange={setTesting} />}
      <ShortcutsDialog open={help} onOpenChange={setHelp} />
    </div>
  );
}
