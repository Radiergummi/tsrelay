import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { expect, test } from "vitest";
import { fixtures } from "@/test/fixtures";
import { renderWithProviders } from "@/test/render";
import { server } from "@/test/server";
import { HookFormPage } from "./HookFormPage";

const invalid = (field: string, message: string) =>
  HttpResponse.json(
    {
      title: "Unprocessable Entity",
      status: 422,
      code: "invalid",
      detail: `${field}: ${message}`,
      errors: [{ field, message }],
    },
    { status: 422, headers: { "Content-Type": "application/problem+json" } },
  );

test("dry-run errors appear under their fields as the user types", async () => {
  server.use(
    http.post("/api/v1/hooks", ({ request }) =>
      new URL(request.url).searchParams.get("dryRun") === "true"
        ? invalid("path", "segments may only contain letters, digits, ., _ and -")
        : HttpResponse.error(),
    ),
  );
  renderWithProviders(<HookFormPage />, {
    route: "/hooks/new",
    path: "/hooks/new",
    context: fixtures.me,
  });
  await userEvent.type(await screen.findByLabelText("Path"), "a b");
  await waitFor(
    () =>
      expect(screen.getByLabelText("Path")).toHaveAccessibleDescription(
        /segments may only contain/,
      ),
    { timeout: 2000 },
  );
  expect(screen.getByLabelText("Path")).toHaveAttribute("aria-invalid", "true");
});

test("hints show the relay's defaults and maxima, and channels come from the grant", async () => {
  renderWithProviders(<HookFormPage />, {
    route: "/hooks/new",
    path: "/hooks/new",
    context: fixtures.me,
  });
  expect(await screen.findByLabelText("Hold timeout")).toHaveAttribute("placeholder", "30s");
  expect(screen.getByText(/At most 5m0s\./)).toBeInTheDocument();
  const options = [...document.querySelectorAll("#channels option")].map((o) =>
    o.getAttribute("value"),
  );
  expect(options).toEqual(["default", "prod"]);
  expect(screen.getByText(/You own \d+ of 10 hooks/)).toBeInTheDocument();
});

test("creating saves, toasts and goes to the new hook", async () => {
  let saved: unknown;
  server.use(
    http.post("/api/v1/hooks", async ({ request }) => {
      if (new URL(request.url).searchParams.get("dryRun") === "true")
        return HttpResponse.json(fixtures.idle);
      saved = await request.json();
      return HttpResponse.json(fixtures.idle, { status: 201 });
    }),
  );
  const { router } = renderWithProviders(<HookFormPage />, {
    route: "/hooks/new",
    path: "/hooks/new",
    context: fixtures.me,
  });
  await userEvent.type(await screen.findByLabelText("Path"), "idle");
  await userEvent.type(screen.getByLabelText("Hold timeout"), "45s");
  await userEvent.click(screen.getByRole("button", { name: "Create hook" }));
  await waitFor(() => expect(router.state.location.pathname).toBe(`/hooks/${fixtures.idle.id}`));
  expect(saved).toEqual({ path: "idle", ephemeral: false, settings: { holdTimeout: "45s" } });
  expect(await screen.findByText("Created idle")).toBeInTheDocument();
});

test("editing prefills the hook and resets cleared fields to the default", async () => {
  let patch: unknown;
  server.use(
    http.patch("/api/v1/hooks/:id", async ({ request }) => {
      if (new URL(request.url).searchParams.get("dryRun") === "true")
        return HttpResponse.json(fixtures.hook);
      patch = await request.json();
      return HttpResponse.json(fixtures.hook);
    }),
  );
  renderWithProviders(<HookFormPage />, {
    route: `/hooks/${fixtures.hook.id}/edit`,
    path: "/hooks/:id/edit",
    context: fixtures.me,
  });
  const desc = await screen.findByLabelText("Description");
  expect(desc).toHaveValue("has a tunnel");
  await userEvent.clear(desc);
  await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() => expect(patch).toMatchObject({ path: "payments/done", description: null }));
});

test("only the owner may edit", async () => {
  renderWithProviders(<HookFormPage />, {
    route: `/hooks/${fixtures.theirs.id}/edit`,
    path: "/hooks/:id/edit",
    context: fixtures.me,
  });
  expect(
    await screen.findByText("There is no hook with this ID, or it is not yours."),
  ).toBeInTheDocument();
});
