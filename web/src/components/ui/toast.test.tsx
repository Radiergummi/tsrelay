import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { toast } from "@/lib/toast";
import { Toaster } from "./toast";

test("shows error toasts with their detail", async () => {
  render(<Toaster />);
  toast.error("Could not delete the hook", "hook not found");
  expect(await screen.findByText("Could not delete the hook")).toBeInTheDocument();
  expect(screen.getByText("hook not found")).toBeInTheDocument();
});
