import { render, screen } from "@testing-library/react";
import { useEffect } from "react";
import { expect, test } from "vitest";
import { toast } from "@/lib/toast";
import { Toaster } from "./toast";

test("shows error toasts with their detail", async () => {
  render(<Toaster />);
  toast.error("Could not delete the hook", "hook not found");
  expect(await screen.findByText("Could not delete the hook")).toBeInTheDocument();
  expect(screen.getByText("hook not found")).toBeInTheDocument();
});

// Base UI's provider subscribes in an effect, and React runs earlier
// siblings' effects first: the Toaster goes before the app (App.tsx) so a
// toast fired while the app mounts isn't emitted to nobody.
test("a toast fired from a later sibling's mount effect shows", async () => {
  function Mounting() {
    useEffect(() => toast.success("Fired on mount"), []);
    return null;
  }
  render(
    <>
      <Toaster />
      <Mounting />
    </>,
  );
  expect(await screen.findByText("Fired on mount")).toBeInTheDocument();
});
