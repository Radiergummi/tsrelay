import { useEffect } from "react";

let open = 0;

/** ModalOpen counts as an open modal while it is mounted; the shared dialogs render one in their popups. */
export function ModalOpen(): null {
  useEffect(() => {
    open++;
    return () => {
      open--;
    };
  }, []);
  return null;
}

/** modalOpen: a modal dialog is open, so it has focus and page shortcuts wait. */
export const modalOpen = (): boolean => open > 0;
