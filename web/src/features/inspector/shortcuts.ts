import { useEffect, useEffectEvent } from "react";
import { modalOpen } from "@/lib/modals";

/** typing reports whether keys go to a text field rather than to the page. */
function typing(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName);
}

/**
 * useShortcuts runs map[event.key] for plain key presses outside text fields,
 * and not while a modal dialog is open: it has focus, and Escape is its own.
 */
export function useShortcuts(map: Record<string, () => void>, enabled = true): void {
  // Runs the latest handlers, without resubscribing the listener.
  const onKey = useEffectEvent((event: KeyboardEvent) => {
    const { ctrlKey, metaKey, altKey, target, key } = event;
    if (ctrlKey || metaKey || altKey || typing(target) || modalOpen()) return;
    const handler = map[key];
    if (handler) {
      event.preventDefault();
      handler();
    }
  });
  useEffect(() => {
    if (!enabled) return;
    const listener = (event: KeyboardEvent) => onKey(event);
    window.addEventListener("keydown", listener);
    return () => window.removeEventListener("keydown", listener);
  }, [enabled]);
}
