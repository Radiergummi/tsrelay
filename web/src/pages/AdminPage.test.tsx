import { QueryClient } from "@tanstack/react-query";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { expect, test } from "vitest";
import { LiveProvider } from "@/lib/live";
import { FakeEventSource } from "@/test/eventSource";
import { fixtures } from "@/test/fixtures";
import { renderWithProviders } from "@/test/render";
import { server } from "@/test/server";
import { AdminPage } from "./AdminPage";

test("lists every hook as metadata, subscribes to every hook's events, and deletes", async () => {
  const deleted: string[] = [];
  server.use(
    http.delete("/hooks/:id", ({ params }) => {
      deleted.push(String(params.id));
      return new HttpResponse(null, { status: 204 });
    }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  renderWithProviders(
    <LiveProvider client={client} user>
      <AdminPage />
    </LiveProvider>,
    { route: "/admin", path: "/admin", context: fixtures.me, client },
  );
  const row = (await screen.findByText("other@example.com")).closest("tr")!;
  expect(within(row).getByRole("link", { name: "theirs" })).toHaveAttribute(
    "href",
    `/hooks/${fixtures.theirs.id}`,
  );
  expect(within(row).getByRole("link", { name: "default" })).toHaveAttribute(
    "href",
    "/channels/default?all=true",
  );
  expect(screen.getByText(/5 hooks, 1 tunnel/)).toBeInTheDocument();
  expect(FakeEventSource.latest()?.url).toBe("/hooks?all=true");

  await userEvent.click(within(row).getByRole("button", { name: "Delete theirs" }));
  await userEvent.click(await screen.findByRole("button", { name: "Delete hook" }));
  await waitFor(() => expect(deleted).toEqual([fixtures.theirs.id]));
  expect(await screen.findByText("Deleted theirs")).toBeInTheDocument();
});

test("another user's hook path is shown with its control characters revealed", async () => {
  server.use(
    http.get("/hooks", () =>
      HttpResponse.json({ hooks: [{ ...fixtures.theirs, path: "pay\u202egnp.exe" }] }),
    ),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  renderWithProviders(
    <LiveProvider client={client} user>
      <AdminPage />
    </LiveProvider>,
    { route: "/admin", path: "/admin", context: fixtures.me, client },
  );
  expect(await screen.findByRole("link", { name: "pay⟨U+202E⟩gnp.exe" })).toBeInTheDocument();
});

test("a non-admin is refused", async () => {
  renderWithProviders(<AdminPage />, {
    route: "/admin",
    path: "/admin",
    context: fixtures.meOther,
  });
  expect(await screen.findByText("Only relay admins can see this page.")).toBeInTheDocument();
});
