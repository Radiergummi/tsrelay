import { Link, useNavigate } from "react-router";
import { useRotatePublicUrl } from "@/api/mutations";
import type { Hook, Me } from "@/api/schemas";
import { Button, buttonVariants } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { Reveal } from "@/features/inspector/Reveal";
import { toast } from "@/lib/toast";
import { DeleteHookDialog } from "./DeleteHookDialog";
import { TunnelText } from "./TunnelText";

export function HookHeader({
  hook,
  me,
  owner,
  onTest,
}: {
  hook: Hook;
  me: Me;
  owner: boolean;
  onTest: () => void;
}) {
  const navigate = useNavigate();
  const rotate = useRotatePublicUrl(hook.id);
  return (
    <header className="mb-4 flex flex-wrap items-start justify-between gap-4 border-b pb-4">
      <div className="min-w-0 space-y-1">
        <h1 className="text-lg font-medium break-all">
          <Reveal text={hook.path} />
        </h1>
        <p className="text-muted-foreground">
          {hook.ephemeral ? "Ephemeral" : "Persistent"}
          {hook.public && (
            <span className="ml-1 rounded-sm bg-amber-100 px-1.5 text-xs text-amber-900 dark:bg-amber-950 dark:text-amber-200">
              Public
            </span>
          )}{" "}
          · channel {hook.channel} · <TunnelText tunnel={hook.tunnel} />
          {!owner && ` · owned by ${hook.owner}`}
        </p>
        {hook.public && !me.funnel && (
          <p className="text-xs text-muted-foreground">
            This relay does not serve hooks on the internet; the hook is public but unreachable
            until Funnel is on.
          </p>
        )}
        {hook.description && (
          <p>
            <Reveal text={hook.description} />
          </p>
        )}
        <p className="flex items-center gap-1">
          <code className="truncate text-xs">{hook.url}</code>
          <CopyButton text={hook.url} label="hook URL" />
        </p>
        {hook.publicUrl && (
          <p className="flex items-center gap-1">
            <span className="text-xs text-muted-foreground">Internet:</span>
            <code className="truncate text-xs">{hook.publicUrl}</code>
            <CopyButton text={hook.publicUrl} label="public URL" />
            {owner && (
              <Button
                size="sm"
                variant="ghost"
                disabled={rotate.isPending}
                onClick={() =>
                  rotate.mutate(undefined, {
                    onSuccess: () => toast.success("The old public URL no longer works"),
                  })
                }
              >
                Rotate public URL
              </Button>
            )}
          </p>
        )}
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
