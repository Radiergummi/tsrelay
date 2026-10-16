import { QueryClient } from "@tanstack/react-query";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { expect, test } from "vitest";
import { LiveProvider } from "@/lib/live";
import { FakeEventSource } from "@/test/eventSource";
import { fixtures } from "@/test/fixtures";
import { renderWithProviders } from "@/test/render";
import { server } from "@/test/server";
import { HookPage } from "./HookPage";

function open(hookId: string, route = `/hooks/${hookId}`, me = fixtures.me) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const ui = (
    <LiveProvider client={client} user>
      <HookPage />
    </LiveProvider>
  );
  return renderWithProviders(ui, {
    route,
    path: "/hooks/:id/calls?/:callId?",
    context: me,
    client,
  });
}

test("shows the hook, its settings, and its calls newest first", async () => {
  open(fixtures.hook.id);
  expect(await screen.findByRole("heading", { name: "payments/done" })).toBeInTheDocument();
  expect(screen.getByText("Hold timeout")).toBeInTheDocument(); // effective settings while nothing is selected
  const rows = await screen.findAllByRole("link", { name: /POST \/payments\/done/ });
  expect(rows.length).toBeGreaterThan(3);
  expect(screen.getByRole("link", { name: "Edit" })).toHaveAttribute(
    "href",
    `/hooks/${fixtures.hook.id}/edit`,
  );
});

test("another user's hook path and description are shown with control characters revealed", async () => {
  server.use(
    http.get("/api/v1/hooks/:id", () =>
      HttpResponse.json({ ...fixtures.theirs, path: "a\u202eb", description: "c\u2066d" }),
    ),
  );
  open(fixtures.theirs.id, `/hooks/${fixtures.theirs.id}`, fixtures.meAdmin);
  expect(await screen.findByRole("heading", { name: "a⟨U+202E⟩b" })).toBeInTheDocument();
  expect(
    screen.getByText((_, el) => el?.tagName === "P" && el.textContent === "c⟨U+2066⟩d"),
  ).toBeInTheDocument();
});

test("filters by status, method, path and sender, kept in the URL", async () => {
  const { router } = open(fixtures.busy.id);
  await screen.findAllByRole("link", { name: /\/busy\/item/ });
  await userEvent.click(screen.getByRole("button", { name: /^5xx/ }));
  expect(router.state.location.search).toBe("?status=5xx");
  for (const row of screen.getAllByRole("link", { name: /\/busy\/item/ }))
    expect(within(row).getByText(/^5\d\d$/)).toBeInTheDocument();
  await userEvent.type(screen.getByRole("searchbox", { name: "Filter by path" }), "item/4");
  await waitFor(() => expect(router.state.location.search).toContain("path=item%2F4"));
});

test("typing a long path fast doesn't lose keystrokes", async () => {
  // RouterProvider applies a navigation inside startTransition; a path
  // input controlled straight from the URL can briefly revert to the
  // pre-keystroke value while that's still committing, dropping fast
  // keystrokes. `delay: null` types with no gap between keys at all, the
  // most adversarial timing userEvent can produce.
  const { router } = open(fixtures.busy.id);
  await screen.findAllByRole("link", { name: /\/busy\/item/ });
  const input = screen.getByRole("searchbox", { name: "Filter by path" });
  await userEvent.type(input, "busy/item/42extra", { delay: null });
  expect(input).toHaveValue("busy/item/42extra");
  await waitFor(() => expect(router.state.location.search).toContain("path=busy%2Fitem%2F42extra"));
});

test("j, k, Enter and Esc move through the calls; / focuses the filter; typing there is not a shortcut", async () => {
  const { router } = open(fixtures.busy.id);
  await screen.findAllByRole("link", { name: /\/busy\/item/ });
  await userEvent.keyboard("j");
  await waitFor(() =>
    expect(router.state.location.pathname).toBe(`/hooks/${fixtures.busy.id}/calls/c_busy_099`),
  );
  await userEvent.keyboard("j");
  await waitFor(() => expect(router.state.location.pathname).toMatch(/c_busy_098$/));
  await userEvent.keyboard("k");
  await waitFor(() => expect(router.state.location.pathname).toMatch(/c_busy_099$/));
  await userEvent.keyboard("{Escape}");
  await waitFor(() => expect(router.state.location.pathname).toBe(`/hooks/${fixtures.busy.id}`));
  await userEvent.keyboard("{Enter}");
  await waitFor(() => expect(router.state.location.pathname).toMatch(/c_busy_099$/));
  await userEvent.keyboard("/");
  expect(screen.getByRole("searchbox", { name: "Filter by path" })).toHaveFocus();
  await userEvent.keyboard("jj");
  expect(router.state.location.pathname).toMatch(/c_busy_099$/);
  await userEvent.keyboard("{Escape}");
  (document.activeElement as HTMLElement).blur();
  await userEvent.keyboard("?");
  expect(await screen.findByRole("dialog", { name: "Keyboard shortcuts" })).toBeInTheDocument();
});

test("shortcuts don't fire while a modal dialog is open", async () => {
  const { router } = open(fixtures.busy.id);
  await screen.findAllByRole("link", { name: /\/busy\/item/ });
  await userEvent.keyboard("?");
  expect(await screen.findByRole("dialog", { name: "Keyboard shortcuts" })).toBeInTheDocument();
  await userEvent.keyboard("t");
  expect(screen.queryByRole("dialog", { name: "Send test request" })).not.toBeInTheDocument();
  const before = router.state.location.pathname;
  await userEvent.keyboard("j");
  expect(router.state.location.pathname).toBe(before);
});

test("a pending call that completes refreshes the open inspector", async () => {
  open(fixtures.hook.id, `/hooks/${fixtures.hook.id}/calls/c_pending`);
  await screen.findByRole("heading", { level: 2, name: /POST/ });
  expect(screen.queryByText("200 OK")).not.toBeInTheDocument();
  const done = { ...fixtures.callPending, outcome: "ok", status: 200, duration: "12ms" };
  server.use(http.get("/api/v1/hooks/:id/calls/c_pending", () => HttpResponse.json(done)));
  act(() => {
    FakeEventSource.latest()!.open();
    FakeEventSource.latest()!.emit("call", { ...done, attempts: undefined });
  });
  expect(await screen.findByText("200 OK")).toBeInTheDocument();
});

test("an admin watching another user's hook gets every hook's events, and no owner controls", async () => {
  open(fixtures.theirs.id, `/hooks/${fixtures.theirs.id}`, fixtures.meAdmin);
  expect(await screen.findByRole("heading", { name: "theirs" })).toBeInTheDocument();
  await waitFor(() => expect(FakeEventSource.latest()?.url).toBe("/api/v1/events?all=true"));
  expect(screen.queryByRole("link", { name: "Edit" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Send test request" })).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Delete" })).toBeInTheDocument();
});

test("a hook deleted while open says so", async () => {
  open(fixtures.idle.id);
  await screen.findByRole("heading", { name: "idle" });
  server.use(
    http.get("/api/v1/hooks/:id", () =>
      HttpResponse.json(
        { status: 404, code: "not_found", detail: "hook not found" },
        { status: 404 },
      ),
    ),
  );
  act(() => FakeEventSource.latest()!.emit("hook", { id: fixtures.idle.id }));
  expect(
    await screen.findByText("There is no hook with this ID, or it is not yours."),
  ).toBeInTheDocument();
});

test("t opens the test request dialog, which shuts on send and toasts the outcome", async () => {
  let sent: unknown;
  server.use(
    http.post("/api/v1/hooks/:id/test", async ({ request }) => {
      sent = await request.json();
      return HttpResponse.json(
        { ...fixtures.callJson, id: "c_test", kind: "test" },
        { status: 201 },
      );
    }),
  );
  const { router } = open(fixtures.hook.id);
  await screen.findByRole("heading", { name: "payments/done" });
  await userEvent.keyboard("t");
  const dialog = await screen.findByRole("dialog", { name: "Send test request" });
  await userEvent.clear(within(dialog).getByLabelText("Path"));
  await userEvent.type(within(dialog).getByLabelText("Path"), "/ping?x=1");
  await userEvent.type(within(dialog).getByLabelText("Headers"), "X-Test: yes");
  await userEvent.type(within(dialog).getByLabelText("Body"), "hello");
  await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));
  await waitFor(() =>
    expect(screen.queryByRole("dialog", { name: "Send test request" })).not.toBeInTheDocument(),
  );
  expect(await screen.findByText(/^Sent: /)).toBeInTheDocument();
  expect(router.state.location.pathname).toBe(`/hooks/${fixtures.hook.id}`);
  expect(sent).toEqual({
    method: "POST",
    path: "/ping?x=1",
    headers: { "X-Test": ["yes"] },
    body: "hello",
  });
});

test("a 422 whose errors don't map to a shown field shows a form-level message in the test dialog", async () => {
  server.use(
    http.post("/api/v1/hooks/:id/test", () =>
      HttpResponse.json(
        {
          status: 422,
          code: "validation",
          title: "Validation failed",
          detail: "the body is too large for this hook",
          errors: [{ field: "body", message: "too large" }],
        },
        { status: 422 },
      ),
    ),
  );
  open(fixtures.hook.id);
  await screen.findByRole("heading", { name: "payments/done" });
  await userEvent.keyboard("t");
  const dialog = await screen.findByRole("dialog", { name: "Send test request" });
  await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));
  // The dialog shuts on send, and the 422 opens it again.
  const reopened = await screen.findByRole("dialog", { name: "Send test request" });
  expect(
    await within(reopened).findByText("the body is too large for this hook"),
  ).toBeInTheDocument();
});

test("a public hook shows its public URL to the owner, who can rotate it", async () => {
  const publicHook = {
    ...fixtures.hook,
    public: true,
    publicUrl: "https://tsrelay.example.ts.net/+old",
  };
  let rotated = false;
  server.use(
    http.get(`/api/v1/hooks/${fixtures.hook.id}`, () =>
      HttpResponse.json(
        rotated ? { ...publicHook, publicUrl: "https://tsrelay.example.ts.net/+new" } : publicHook,
      ),
    ),
    http.post(`/api/v1/hooks/${fixtures.hook.id}/rotate-public-url`, () => {
      rotated = true;
      return HttpResponse.json({ ...publicHook, publicUrl: "https://tsrelay.example.ts.net/+new" });
    }),
  );
  open(fixtures.hook.id);
  expect(await screen.findByText("https://tsrelay.example.ts.net/+old")).toBeInTheDocument();
  expect(screen.getByText("Public")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Rotate public URL" }));
  expect(await screen.findByText("https://tsrelay.example.ts.net/+new")).toBeInTheDocument();
});

test("a public hook explains that it's unreachable while this relay runs without Funnel", async () => {
  server.use(
    http.get(`/api/v1/hooks/${fixtures.hook.id}`, () =>
      HttpResponse.json({ ...fixtures.hook, public: true }),
    ),
  );
  open(fixtures.hook.id); // fixtures.me has no funnel field, i.e. Funnel is off
  expect(await screen.findByText("Public")).toBeInTheDocument();
  expect(screen.getByText(/does not serve hooks on the internet/i)).toBeInTheDocument();
});
