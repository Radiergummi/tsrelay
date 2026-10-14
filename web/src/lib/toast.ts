interface ToastOptions {
  title: string;
  type: "success" | "error";
  timeout?: number;
  description?: string;
}

async function show(opts: ToastOptions): Promise<void> {
  const { toastManager } = await import("./toastRuntime");
  toastManager.add(opts);
}

/** toast lets code outside React (mutation callbacks) show toasts, without statically pulling in the Base UI toast runtime. */
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
