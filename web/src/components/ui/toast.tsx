import { Toast } from "@base-ui/react/toast";
import { X } from "lucide-react";
import { toastManager } from "@/lib/toastRuntime";
import { cn } from "@/lib/utils";

function ToastList() {
  const { toasts } = Toast.useToastManager();
  return toasts.map((t) => (
    <Toast.Root
      key={t.id}
      toast={t}
      className={cn(
        "relative rounded-md border bg-popover py-2.5 pr-8 pl-3 text-popover-foreground shadow-sm",
        t.type === "error" && "border-destructive/40",
      )}
    >
      <Toast.Title className="font-medium" />
      <Toast.Description className="mt-0.5 text-muted-foreground" />
      <Toast.Close
        aria-label="Dismiss"
        className="absolute top-2 right-2 rounded-sm p-0.5 text-muted-foreground hover:text-foreground"
      >
        <X className="size-3.5" />
      </Toast.Close>
    </Toast.Root>
  ));
}

/**
 * Toaster shows toasts in the bottom right. It's a sibling of the app, not a
 * wrapper: toasts are added through the shared toastManager, not React
 * context, so nothing needs to render underneath it.
 */
export function Toaster() {
  return (
    <Toast.Provider toastManager={toastManager} timeout={5000}>
      <Toast.Portal>
        <Toast.Viewport className="fixed right-4 bottom-4 z-50 flex w-80 flex-col gap-2">
          <ToastList />
        </Toast.Viewport>
      </Toast.Portal>
    </Toast.Provider>
  );
}
