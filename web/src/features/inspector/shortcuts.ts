import { useEffect, useRef } from "react";

/** typing reports whether keys go to a text field rather than to the page. */
function typing(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName);
}

/**
 * modalOpen: a Base UI Dialog or AlertDialog popup (role="dialog" or
 * "alertdialog") is in the document. Those trap focus while open, so no
 * page shortcut should fire behind them; Escape stays Base UI's own to
 * dismiss the dialog. Base UI's toast reuses the same roles but marks
 * itself aria-modal="false" (it never takes focus), so it's excluded here
 * and never blocks a shortcut.
 */
function modalOpen(): boolean {
  return (
    document.querySelector(
      '[role="dialog"]:not([aria-modal="false"]), [role="alertdialog"]:not([aria-modal="false"])',
    ) !== null
  );
}

/** useShortcuts runs map[event.key] for plain key presses outside text fields and modal dialogs. */
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
