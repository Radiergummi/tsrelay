import { lazy, Suspense } from "react";
import { useParams } from "react-router";
import { ApiError } from "@/api/client";
import { useHook } from "@/api/queries";
import { EmptyState, PageError, TableSkeleton } from "@/components/PageState";
import { useViewer } from "@/lib/useViewer";
import { isReceiver, owns } from "@/lib/viewer";
import { NoAccessPage } from "./NoAccessPage";

const HookForm = lazy(async () => ({
  default: (await import("@/features/hooks/HookForm")).HookForm,
}));

export function HookFormPage() {
  const me = useViewer();
  const { id } = useParams();
  if (!isReceiver(me)) return <NoAccessPage me={me} />;
  return (
    <section>
      <h1 className="mb-4 text-lg font-medium">{id ? "Edit hook" : "New hook"}</h1>
      <Suspense fallback={<TableSkeleton />}>
        {id ? <Edit id={id} /> : <HookForm me={me} />}
      </Suspense>
    </section>
  );
}

/** Edit is for the hook's owner only: admins cannot change other users' hooks (DESIGN.md §5.2). */
function Edit({ id }: { id: string }) {
  const me = useViewer();
  const hook = useHook(id);
  if (hook.isPending) return <TableSkeleton />;
  if (hook.isError && !(hook.error instanceof ApiError && hook.error.status === 404))
    return <PageError error={hook.error} />;
  if (hook.isError || !owns(me, hook.data))
    return <EmptyState title="There is no hook with this ID, or it is not yours." />;
  return <HookForm me={me} hook={hook.data} />;
}
