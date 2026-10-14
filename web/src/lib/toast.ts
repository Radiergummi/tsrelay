interface ToastOptions {
  title: string;
  type: "success" | "error";
  timeout?: number;
  description?: string;
}

async function show(opts: ToastOptions): Promise<void> {
  // A toast is never worth an unhandled rejection: if the chunk fails to
  // load (offline, blocked), the toast is silently dropped instead.
  const mod = await import("./toastRuntime").catch(() => undefined);
  mod?.toastManager.add(opts);
}

/**
 * toast lets code outside React (mutation callbacks) show toasts, without
 * statically pulling in the Base UI toast runtime. A toast shown before the
 * <Toaster/> has mounted (and subscribed to the shared toastManager) is
 * dropped — not a concern today, since App.tsx mounts it immediately.
 */
export const toast = {
  success(title: string) {
    void show({ title, type: "success" });
  },
  error(title: string, description?: string) {
    void show({
      title,
      type: "error",
      timeout: 8000,
      ...(description ? { description } : {}),
    });
  },
};
