import { screen, within } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { expect, test } from "vitest";
import { fixtures } from "@/test/fixtures";
import { renderWithProviders } from "@/test/render";
import { server } from "@/test/server";
import { HooksPage } from "./HooksPage";

// The layout normally provides the entry document as the outlet context; context does that here.
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

test("explains how to install the CLI and get a first hook", async () => {
  server.use(http.get("/hooks", () => HttpResponse.json([])));
  renderPage();
  expect(
    await screen.findByText(`curl -fsSL ${window.location.origin}/install.sh | sh`),
  ).toBeInTheDocument();
  expect(screen.getByText(/tsrelay http 8080/)).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Other ways to install" })).toHaveAttribute(
    "href",
    "/cli",
  );
  expect(screen.getByRole("link", { name: "create one here" })).toHaveAttribute(
    "href",
    "/hooks/new",
  );
});
