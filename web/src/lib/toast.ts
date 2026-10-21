import { toastManager } from "@/components/ui/toast";

/** toast lets code outside React (mutation callbacks) show toasts in the <Toaster/>. */
export const toast = {
  success(title: string) {
    toastManager.add({ title, type: "success" });
  },
  error(title: string, description?: string) {
    toastManager.add({
      title,
      type: "error",
      timeout: 8000,
      ...(description ? { description } : {}),
    });
  },
};
