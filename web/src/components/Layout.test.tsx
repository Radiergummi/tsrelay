import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { createMemoryRouter, RouterProvider } from "react-router";
import { expect, test } from "vitest";
import { routes } from "@/routes";
import { FakeEventSource } from "@/test/eventSource";
import { fixtures } from "@/test/fixtures";
import { server } from "@/test/server";

function renderAt(
  path: string,
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } }),
) {
  const router = createMemoryRouter(routes, { initialEntries: [path] });
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}
const meIs = (me: Record<string, unknown>, status = 200) =>
  server.use(http.get("/", () => HttpResponse.json(me, { status })));

test("a receiver who is an admin sees both sections and their identity", async () => {
  renderAt("/");
  expect(await screen.findByRole("link", { name: "Hooks" })).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Channels" })).toHaveAttribute("href", "/channels");
  expect(screen.getByRole("link", { name: "Admin" })).toBeInTheDocument();
  expect(screen.getByText("dev@example.com")).toBeInTheDocument();
});

test("an admin without the receiver role starts on /admin", async () => {
  meIs(fixtures.meAdmin);
  const router = renderAt("/");
  await waitFor(() => expect(router.state.location.pathname).toBe("/admin"));
  expect(screen.queryByRole("link", { name: "Hooks" })).not.toBeInTheDocument();
});

test("a user without a role sees what is missing and an example grant", async () => {
  meIs(fixtures.meNobody);
  renderAt("/hooks/anything");
  expect(await screen.findByText(/grants you no access to this relay/)).toBeInTheDocument();
  const grant = screen.getByText(/"grants"/);
  expect(grant.textContent).toContain('"nobody@example.com"');
  expect(grant.textContent).toContain('"mazetti.me/cap/relay"');
});

// The relay refuses event streams without a role (403), so asking would
// only retry forever.
test("a user without a role opens no event stream", async () => {
  meIs(fixtures.meNobody);
  renderAt("/hooks");
  expect(await screen.findByText(/grants you no access to this relay/)).toBeInTheDocument();
  expect(FakeEventSource.instances).toHaveLength(0);
});

test("a user with a role opens one", async () => {
  renderAt("/hooks");
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
});

test("the footer names the relay's version and commit", async () => {
  meIs({ ...fixtures.me, version: "v0.3.0", commit: "0123456789abcdef" });
  renderAt("/hooks");
  expect(await screen.findByText("v0.3.0")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "(0123456)" })).toBeInTheDocument();
});

test("the footer is under the no-access state", async () => {
  meIs(fixtures.meNobody);
  renderAt("/hooks");
  await screen.findByText(/grants you no access to this relay/);
  expect(screen.getByRole("link", { name: "Licenses" })).toBeInTheDocument();
});

test("a user without a role can read the licenses", async () => {
  meIs(fixtures.meNobody);
  server.use(
    http.get("/licenses", () =>
      HttpResponse.json({
        components: [{ name: "react", ecosystem: "npm", licenses: [{ id: "MIT" }] }],
      }),
    ),
  );
  renderAt("/licenses");
  expect(await screen.findByRole("heading", { name: "Open-source licenses" })).toBeInTheDocument();
  expect(await screen.findByText("react")).toBeInTheDocument();
  expect(screen.queryByText(/grants you no access/)).not.toBeInTheDocument();
});

test("a tagged node is told it cannot own hooks", async () => {
  meIs({ ...fixtures.meNobody, login: undefined, tags: ["tag:ci"], roles: { sender: ["*"] } });
  renderAt("/");
  expect(await screen.findByText(/Tagged nodes cannot own hooks/)).toBeInTheDocument();
  expect(screen.getByText(/tag:ci/)).toBeInTheDocument();
});

test("a tagged node with the admin role gets no admin section", async () => {
  meIs({ ...fixtures.meAdmin, login: undefined, tags: ["tag:ops"] });
  renderAt("/admin");
  expect(await screen.findByText(/Tagged nodes cannot own hooks/)).toBeInTheDocument();
  expect(screen.queryByRole("link", { name: "Admin" })).not.toBeInTheDocument();
});

test("an unidentified viewer gets one message and a reload button", async () => {
  meIs(
    {
      title: "Forbidden",
      status: 403,
      code: "forbidden",
      detail: "could not identify you on the tailnet",
    },
    403,
  );
  renderAt("/");
  expect(await screen.findByText(/could not identify your device/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Reload" })).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Licenses" })).toBeInTheDocument();
});

test("a failed refetch of the entry document keeps the app", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  renderAt("/hooks", client);
  expect(await screen.findByRole("link", { name: "Hooks" })).toBeInTheDocument();
  meIs({ status: 500, code: "internal", detail: "boom" }, 500);
  await act(() => client.refetchQueries({ queryKey: ["me"] }));
  expect(client.getQueryState(["me"])?.status).toBe("error");
  expect(screen.queryByText("The relay is not reachable")).not.toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Hooks" })).toBeInTheDocument();
});

test("unknown pages and expired inspect links", async () => {
  renderAt("/no/such/page");
  expect(await screen.findByText("There is no page here.")).toBeInTheDocument();
});

test("an expired inspect link says so", async () => {
  renderAt("/inspect/c_gone");
  expect(await screen.findByText("This call has expired from the inspector")).toBeInTheDocument();
});
