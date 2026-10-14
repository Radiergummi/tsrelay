import { Link, useNavigate } from "react-router";
import type { Hook } from "@/api/schemas";
import { Button, buttonVariants } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { DeleteHookDialog } from "./DeleteHookDialog";
import { TunnelText } from "./TunnelText";

export function HookHeader({
  hook,
  owner,
  onTest,
}: {
  hook: Hook;
  owner: boolean;
  onTest: () => void;
}) {
  const navigate = useNavigate();
  return (
    <header className="mb-4 flex flex-wrap items-start justify-between gap-4 border-b pb-4">
      <div className="min-w-0 space-y-1">
        <h1 className="text-lg font-medium break-all">{hook.path}</h1>
        <p className="text-muted-foreground">
          {hook.ephemeral ? "Ephemeral" : "Persistent"} · channel {hook.channel} ·{" "}
          <TunnelText tunnel={hook.tunnel} />
          {!owner && ` · owned by ${hook.owner}`}
        </p>
        {hook.description && <p>{hook.description}</p>}
        <p className="flex items-center gap-1">
          <code className="truncate text-xs">{hook.url}</code>
          <CopyButton text={hook.url} label="hook URL" />
        </p>
      </div>
      <div className="flex gap-2">
        {owner && (
          <>
            <Button size="sm" onClick={onTest}>
              Send test request
            </Button>
            <Link
              to={`/hooks/${encodeURIComponent(hook.id)}/edit`}
              className={buttonVariants({ variant: "outline", size: "sm" })}
            >
              Edit
            </Link>
          </>
        )}
        <DeleteHookDialog
          hook={hook}
          onDeleted={() => navigate(owner ? "/" : "/admin")}
          trigger={
            <Button size="sm" variant="ghost">
              Delete
            </Button>
          }
        />
      </div>
    </header>
  );
}
