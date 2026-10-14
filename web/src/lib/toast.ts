import { Toast } from "@base-ui/react/toast";

/** toastManager lets code outside React (mutation callbacks) show toasts. */
export const toastManager = Toast.createToastManager();

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
