import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { expect, test } from "vitest";
import { keys } from "@/api/queries";
import { makeQueryClient } from "@/api/queryClient";
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
    http.post("/hooks", ({ request }) =>
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
  await userEvent.click(await screen.findByRole("button", { name: /Delivery/ }));
  const hold = screen.getByRole("radiogroup", { name: "Hold timeout" });
  expect(within(hold).getByRole("radio", { checked: true })).toHaveTextContent("30 sdefault");
  expect(screen.getByText(/At most 5 min; longer presets are hidden\./)).toBeInTheDocument();
  await userEvent.click(screen.getByRole("combobox", { name: "Channel" }));
  const listbox = await screen.findByRole("listbox");
  await waitFor(() =>
    expect(
      within(listbox)
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual(["default", "prod"]),
  );
  expect(screen.getByText(/You own \d+ of 10 hooks/)).toBeInTheDocument();
});

test("creating saves, toasts and goes to the new hook", async () => {
  let saved: unknown;
  server.use(
    http.post("/hooks", async ({ request }) => {
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
  await userEvent.click(screen.getByRole("button", { name: /Delivery/ }));
  await userEvent.click(screen.getByRole("radio", { name: "2 min" }));
  await userEvent.click(screen.getByRole("button", { name: "Create hook" }));
  await waitFor(() => expect(router.state.location.pathname).toBe(`/hooks/${fixtures.idle.id}`));
  expect(saved).toEqual({
    path: "idle",
    ephemeral: false,
    public: false,
    settings: { holdTimeout: "2m" },
  });
  expect(await screen.findByText("Created idle")).toBeInTheDocument();
});

// The app's query client toasts failed writes other than 422s; the form
// doesn't repeat them.
test("delivery starts shut, and opens for an error in one of its fields", async () => {
  server.use(
    http.post("/hooks", () =>
      HttpResponse.json(
        {
          status: 422,
          code: "validation",
          title: "Validation failed",
          errors: [{ field: "settings.holdTimeout", message: "must be at most 5m0s" }],
        },
        { status: 422 },
      ),
    ),
  );
  renderWithProviders(<HookFormPage />, {
    route: "/hooks/new",
    path: "/hooks/new",
    context: fixtures.me,
  });
  const trigger = await screen.findByRole("button", { name: /Delivery/ });
  expect(trigger).toHaveAttribute("aria-expanded", "false");
  expect(screen.queryByLabelText("Hold timeout")).not.toBeInTheDocument();
  await userEvent.type(screen.getByLabelText("Path"), "idle");
  expect(await screen.findByText("must be at most 5m0s")).toBeInTheDocument();
  expect(trigger).toHaveAttribute("aria-expanded", "true");
});

test("a failed save other than a 422 is reported once, by the toast", async () => {
  server.use(
    http.post("/hooks", ({ request }) =>
      new URL(request.url).searchParams.get("dryRun") === "true"
        ? HttpResponse.json(fixtures.idle)
        : HttpResponse.json(
            {
              status: 500,
              code: "internal",
              title: "Internal Server Error",
              detail: "the disk is full",
            },
            { status: 500, headers: { "Content-Type": "application/problem+json" } },
          ),
    ),
  );
  renderWithProviders(<HookFormPage />, {
    route: "/hooks/new",
    path: "/hooks/new",
    context: fixtures.me,
    client: makeQueryClient(),
  });
  await userEvent.type(await screen.findByLabelText("Path"), "idle");
  await userEvent.click(screen.getByRole("button", { name: "Create hook" }));
  expect(await screen.findByText("That didn't work")).toBeInTheDocument();
  expect(screen.getAllByText(/the disk is full/)).toHaveLength(1);
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

// A dry run is no mutation, so nothing else reports its failure.
test("a dry run that can't reach the relay says so in the form", async () => {
  server.use(http.post("/hooks", () => HttpResponse.error()));
  renderWithProviders(<HookFormPage />, {
    route: "/hooks/new",
    path: "/hooks/new",
    context: fixtures.me,
    client: makeQueryClient(),
  });
  await userEvent.type(await screen.findByLabelText("Path"), "idle");
  expect(await screen.findByRole("alert")).toHaveTextContent(/The relay is not reachable/);
});

test("a form-level error goes away once the next change fails local validation", async () => {
  server.use(http.post("/hooks", () => invalid("settings.unknown", "the relay refused this")));
  renderWithProviders(<HookFormPage />, {
    route: "/hooks/new",
    path: "/hooks/new",
    context: fixtures.me,
  });
  await userEvent.type(await screen.findByLabelText("Path"), "idle");
  expect(await screen.findByRole("alert")).toHaveTextContent(/the relay refused this/);
  await userEvent.click(screen.getByRole("button", { name: /Delivery/ }));
  await userEvent.click(screen.getAllByRole("radio", { name: "custom…" })[0]!);
  await userEvent.type(screen.getByLabelText("Hold timeout, custom"), "soon");
  await waitFor(() =>
    expect(screen.getByLabelText("Hold timeout, custom")).toHaveAccessibleDescription(
      /must be a duration/,
    ),
  );
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

test("editing prefills the hook and sends only what changed, resetting cleared fields", async () => {
  let patch: unknown;
  let dryRun: unknown;
  server.use(
    http.patch("/hooks/:id", async ({ request }) => {
      if (new URL(request.url).searchParams.get("dryRun") === "true") {
        dryRun = await request.json();
        return HttpResponse.json(fixtures.hook);
      }
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
  await waitFor(() => expect(dryRun).toEqual({ description: null }));
  await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() => expect(patch).toEqual({ description: null }));
});

test("a failed refetch of the hook being edited keeps the unsaved edits", async () => {
  const { client } = renderWithProviders(<HookFormPage />, {
    route: `/hooks/${fixtures.hook.id}/edit`,
    path: "/hooks/:id/edit",
    context: fixtures.me,
  });
  const desc = await screen.findByLabelText("Description");
  await userEvent.type(desc, " and more");
  server.use(
    http.get("/hooks/:id", () =>
      HttpResponse.json({ status: 500, code: "internal", detail: "boom" }, { status: 500 }),
    ),
  );
  await act(() => client.refetchQueries({ queryKey: keys.hook(fixtures.hook.id) }));
  expect(client.getQueryState(keys.hook(fixtures.hook.id))?.status).toBe("error");
  // The query notifies the page on the next tick, after the refetch settles.
  await act(() => new Promise((r) => setTimeout(r, 0)));
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  expect(screen.getByLabelText("Description")).toHaveValue("has a tunnel and more");
});

test("the internet choice is disabled without the grant or without Funnel", async () => {
  renderWithProviders(<HookFormPage />, {
    route: "/hooks/new",
    path: "/hooks/new",
    context: { ...fixtures.me, public: [], funnel: true },
  });
  // Base UI's Radio is a <span role="radio">, which jest-dom's
  // toBeDisabled/toBeEnabled don't recognize (they only check native form
  // tags), so this asserts the same thing through aria-disabled instead.
  expect(await screen.findByRole("radio", { name: "Tailnet and internet" })).toHaveAttribute(
    "aria-disabled",
    "true",
  );
  expect(screen.getByText(/"public": true/)).toBeInTheDocument();
});

test("the internet choice is enabled with the grant and Funnel", async () => {
  renderWithProviders(<HookFormPage />, {
    route: "/hooks/new",
    path: "/hooks/new",
    context: { ...fixtures.me, public: ["*"], funnel: true },
  });
  expect(await screen.findByRole("radio", { name: "Tailnet and internet" })).not.toHaveAttribute(
    "aria-disabled",
    "true",
  );
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
