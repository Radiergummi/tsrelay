import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { createMemoryRouter, RouterProvider } from "react-router";
import { expect, test } from "vitest";
import { routes } from "@/routes";
import { fixtures } from "@/test/fixtures";
import { server } from "@/test/server";

function renderAt(path: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
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

test("a tagged node is told it cannot own hooks", async () => {
  meIs({ ...fixtures.meNobody, login: undefined, tags: ["tag:ci"], roles: { sender: ["*"] } });
  renderAt("/");
  expect(await screen.findByText(/Tagged nodes cannot own hooks/)).toBeInTheDocument();
  expect(screen.getByText(/tag:ci/)).toBeInTheDocument();
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
});

test("unknown pages and expired inspect links", async () => {
  renderAt("/no/such/page");
  expect(await screen.findByText("There is no page here.")).toBeInTheDocument();
});

test("an expired inspect link says so", async () => {
  renderAt("/inspect/c_gone");
  expect(await screen.findByText("This call has expired from the inspector")).toBeInTheDocument();
});
