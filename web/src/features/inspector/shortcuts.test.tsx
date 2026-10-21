import { render } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { expect, test, vi } from "vitest";
import { AlertDialog, AlertDialogContent, AlertDialogTitle } from "@/components/ui/alert-dialog";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { useShortcuts } from "./shortcuts";

function Page({ onR, children }: { onR: () => void; children?: ReactNode }) {
  useShortcuts({ r: onR });
  return <>{children}</>;
}

test("shortcuts fire on the page", async () => {
  const onR = vi.fn<() => void>();
  render(<Page onR={onR} />);
  await userEvent.keyboard("r");
  expect(onR).toHaveBeenCalledOnce();
});

test("an open dialog holds the page's shortcuts", async () => {
  const onR = vi.fn<() => void>();
  render(
    <Page onR={onR}>
      <Dialog open>
        <DialogContent>
          <DialogTitle>Open</DialogTitle>
        </DialogContent>
      </Dialog>
    </Page>,
  );
  await userEvent.keyboard("r");
  expect(onR).not.toHaveBeenCalled();
});

test("an open alert dialog holds the page's shortcuts", async () => {
  const onR = vi.fn<() => void>();
  render(
    <Page onR={onR}>
      <AlertDialog open>
        <AlertDialogContent>
          <AlertDialogTitle>Sure?</AlertDialogTitle>
        </AlertDialogContent>
      </AlertDialog>
    </Page>,
  );
  await userEvent.keyboard("r");
  expect(onR).not.toHaveBeenCalled();
});

// Only our modal dialogs hold shortcuts: other things that call themselves
// a dialog (a toast, a popover) don't trap focus.
test("an element that merely has a dialog role doesn't hold them", async () => {
  const onR = vi.fn<() => void>();
  render(
    <Page onR={onR}>
      <div role="alertdialog">not modal</div>
    </Page>,
  );
  await userEvent.keyboard("r");
  expect(onR).toHaveBeenCalledOnce();
});

test("closing the dialog gives the shortcuts back", async () => {
  const onR = vi.fn<() => void>();
  const { rerender } = render(
    <Page onR={onR}>
      <Dialog open>
        <DialogContent>
          <DialogTitle>Open</DialogTitle>
        </DialogContent>
      </Dialog>
    </Page>,
  );
  rerender(
    <Page onR={onR}>
      <Dialog open={false}>
        <DialogContent>
          <DialogTitle>Open</DialogTitle>
        </DialogContent>
      </Dialog>
    </Page>,
  );
  await vi.waitFor(() => expect(document.querySelector('[data-slot="dialog-content"]')).toBeNull());
  await userEvent.keyboard("r");
  expect(onR).toHaveBeenCalledOnce();
});
