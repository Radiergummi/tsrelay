import { Toast } from "@base-ui/react/toast";

/**
 * toastRuntime is loaded lazily: @base-ui/react/toast exports one namespace
 * object ("export * as Toast"), so any static import of it pulls in its
 * whole component tree (Provider, Root, Viewport, ...), not just
 * createToastManager. @/lib/toast imports this dynamically so the entry
 * chunk never pays for it.
 */
const manager = Toast.createToastManager();

/**
 * Base UI's manager has no buffer of its own: add() before any
 * <Toast.Provider> has subscribed is emitted to nobody and lost. The
 * <Toaster/> is lazy-loaded (bundle budget), so a toast fired right after
 * navigation can race its mount; queue those and replay them once a
 * provider subscribes.
 */
const pending: Parameters<typeof manager.add>[0][] = [];
let subscribed = false;

export const toastManager: typeof manager = {
  ...manager,
  add(options) {
    if (!subscribed) {
      pending.push(options);
      return options.id ?? "";
    }
    return manager.add(options);
  },
  " subscribe"(listener) {
    subscribed = true;
    const unsubscribe = manager[" subscribe"](listener);
    for (const options of pending.splice(0)) manager.add(options);
    return () => {
      subscribed = false;
      unsubscribe();
    };
  },
};
