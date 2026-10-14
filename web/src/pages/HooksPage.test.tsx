import { screen, within } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { expect, test } from "vitest";
import { fixtures } from "@/test/fixtures";
import { renderWithProviders } from "@/test/render";
import { server } from "@/test/server";
import { HooksPage } from "./HooksPage";

// The layout normally provides /me as the outlet context; context does that here.
function renderPage() {
  return renderWithProviders(<HooksPage />, { route: "/", path: "/", context: fixtures.me });
}

test("lists the viewer's hooks with tunnel, last call and URL", async () => {
  renderPage();
  const row = (await screen.findByRole("link", { name: "payments/done" })).closest("tr")!;
  expect(within(row).getByText(/dev-laptop/)).toBeInTheDocument();
  expect(within(row).getByText(/POST \/payments\/done/)).toBeInTheDocument();
  expect(within(row).getByRole("button", { name: "Copy hook URL" })).toBeInTheDocument();
  const idle = screen.getByRole("link", { name: "idle" }).closest("tr")!;
  expect(within(idle).getByText("not attached")).toBeInTheDocument();
  expect(within(idle).getByText("prod")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "New hook" })).toHaveAttribute("href", "/hooks/new");
});

test("explains how to get a first hook", async () => {
  server.use(http.get("/api/v1/hooks", () => HttpResponse.json([])));
  renderPage();
  expect(await screen.findByText(/tsrelay http 8080/)).toBeInTheDocument();
});
