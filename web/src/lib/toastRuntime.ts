import { Toast } from "@base-ui/react/toast";

/**
 * toastRuntime is loaded lazily: @base-ui/react/toast exports one namespace
 * object ("export * as Toast"), so any static import of it pulls in its
 * whole component tree (Provider, Root, Viewport, ...), not just
 * createToastManager. @/lib/toast imports this dynamically so the entry
 * chunk never pays for it.
 */
export const toastManager = Toast.createToastManager();
