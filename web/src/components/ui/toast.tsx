import { Toast } from "@base-ui/react/toast";
import { X } from "lucide-react";
import type { ReactNode } from "react";
import { toastManager } from "@/lib/toast";
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

/** Toaster provides toasts to children and shows them in the bottom right. */
export function Toaster({ children }: { children: ReactNode }) {
  return (
    <Toast.Provider toastManager={toastManager} timeout={5000}>
      {children}
      <Toast.Portal>
        <Toast.Viewport className="fixed right-4 bottom-4 z-50 flex w-80 flex-col gap-2">
          <ToastList />
        </Toast.Viewport>
      </Toast.Portal>
    </Toast.Provider>
  );
}
