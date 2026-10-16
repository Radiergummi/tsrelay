import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { StatusText } from "./StatusText";

test("a status of the relay's 56x range is named and set apart from the app's", () => {
  const { rerender } = render(<StatusText call={{ outcome: "tunnel_lost", status: 563 }} />);
  expect(screen.getByText("563 Tunnel lost")).toHaveClass("font-semibold", "text-status-5xx");

  rerender(<StatusText call={{ outcome: "tunnel_lost", status: 563 }} short />);
  expect(screen.getByText("563")).toHaveAttribute("title", "Tunnel lost");

  rerender(<StatusText call={{ outcome: "ok", status: 502 }} />);
  expect(screen.getByText("502 Bad Gateway")).not.toHaveClass("font-semibold");
});
