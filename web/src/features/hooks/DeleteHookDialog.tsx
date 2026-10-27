import { useState, type ReactElement } from "react";
import { useDeleteHook } from "@/api/mutations";
import type { Hook } from "@/api/schemas";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { toast } from "@/lib/toast";

/** DeleteHookDialog asks before deleting a hook. trigger is the button that opens it. */
export function DeleteHookDialog({
  hook,
  trigger,
  onDeleted,
}: {
  hook: Hook;
  trigger: ReactElement;
  onDeleted?: () => void;
}) {
  const [open, setOpen] = useState(false);
  const deletion = useDeleteHook();
  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger render={trigger} />
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {hook.path}?</AlertDialogTitle>
          <AlertDialogDescription>
            Its URL stops working at once and its calls are forgotten. This can't be undone.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <Button
            variant="destructive"
            disabled={deletion.isPending}
            onClick={() =>
              deletion.mutate(hook.id, {
                onSuccess: () => {
                  setOpen(false);
                  toast.success(`Deleted ${hook.path}`);
                  onDeleted?.();
                },
              })
            }
          >
            Delete hook
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
