import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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
  server.use(http.get("/hooks", () => HttpResponse.json({ hooks: [] })));
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

test("channel names link to their channel's page", async () => {
  renderPage();
  const idle = (await screen.findByRole("link", { name: "idle" })).closest("tr")!;
  expect(within(idle).getByRole("link", { name: "prod" })).toHaveAttribute(
    "href",
    "/channels/prod",
  );
});

test("the channel filter asks the relay for that channel's hooks and keeps it in the URL", async () => {
  const asked: string[] = [];
  const onRequest = ({ request }: { request: Request }) => {
    const url = new URL(request.url);
    if (url.pathname === "/hooks") asked.push(url.search);
  };
  server.events.on("request:start", onRequest);
  const { router } = renderPage();
  await screen.findByRole("link", { name: "payments/done" });
  const filter = screen.getByRole("combobox", { name: "Channel" });
  await within(filter).findByRole("option", { name: "prod" });
  await userEvent.selectOptions(filter, "prod");
  await waitFor(() =>
    expect(screen.queryByRole("link", { name: "payments/done" })).not.toBeInTheDocument(),
  );
  expect(screen.getByRole("link", { name: "idle" })).toBeInTheDocument();
  expect(asked).toContain("?channel=prod");
  expect(router.state.location.search).toBe("?channel=prod");
  server.events.removeListener("request:start", onRequest);
});

test("a filtered list without hooks says so", async () => {
  renderWithProviders(<HooksPage />, {
    route: "/?channel=staging.eu",
    path: "/",
    context: fixtures.me,
  });
  expect(await screen.findByText("You have no hooks in staging.eu")).toBeInTheDocument();
  expect(screen.getByRole("combobox", { name: "Channel" })).toHaveValue("staging.eu");
});
