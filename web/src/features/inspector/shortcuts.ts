import { useEffect, useRef } from "react";
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
  const current = useRef(map);
  // oxlint-disable-next-line react/refs -- updates every render so the listener always runs the latest handlers, without resubscribing it.
  current.current = map;
  useEffect(() => {
    if (!enabled) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.ctrlKey || e.metaKey || e.altKey || typing(e.target) || modalOpen()) return;
      const fn = current.current[e.key];
      if (fn) {
        e.preventDefault();
        fn();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [enabled]);
}
