import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { afterEach, expect, test } from "vitest";
import type { Hook, Me } from "@/api/schemas";
import { fixtures } from "@/test/fixtures";
import { renderWithProviders } from "@/test/render";
import { server } from "@/test/server";
import { HookForm } from "./HookForm";

/** render shows the form for me, creating or (with hook) editing; saved holds the body of the last save. */
function render(me: Me = fixtures.me, hook?: Hook) {
  const saved: { body?: Record<string, unknown> } = {};
  const answer = async ({ request }: { request: Request }) => {
    const body = (await request.json()) as Record<string, unknown>;
    if (new URL(request.url).searchParams.get("dryRun") !== "true") saved.body = body;
    // A dry run answers with the hook the body would give.
    const settings = (body["settings"] ?? {}) as Record<string, unknown>;
    return HttpResponse.json({
      ...(hook ?? fixtures.idle),
      path: body["path"] ?? hook?.path ?? "idle",
      effective: { ...me.policy.defaults, ...settings },
    });
  };
  server.use(http.post("/hooks", answer), http.patch("/hooks/:id", answer));
  renderWithProviders(<HookForm me={me} {...(hook ? { hook } : {})} />, {
    route: "/hooks/new",
    path: "/hooks/new",
  });
  return saved;
}

const openDelivery = () => userEvent.click(screen.getByRole("button", { name: /Delivery/ }));
const group = (name: string) => screen.getByRole("radiogroup", { name });
const radio = (groupName: string, name: string | RegExp) =>
  within(group(groupName)).getByRole("radio", { name });
const submit = () => userEvent.click(screen.getByRole("button", { name: /Create hook|Save/ }));
const withMax = (max: Partial<Me["policy"]["max"]>): Me => ({
  ...fixtures.me,
  policy: { ...fixtures.me.policy, max: { ...fixtures.me.policy.max, ...max } },
});

test("duration presets above the relay's maximum are hidden, and the default is marked", async () => {
  render(withMax({ holdTimeout: "30s" }));
  await openDelivery();
  const hold = within(group("Hold timeout"))
    .getAllByRole("radio")
    .map((r) => r.textContent);
  expect(hold).toEqual(["off", "10 s", "30 sdefault", "custom…"]);
  expect(radio("Hold timeout", /30 s/)).toHaveAttribute("aria-checked", "true");
  expect(radio("Response timeout", "30 min")).toBeInTheDocument();
  expect(screen.getByText(/At most 30 s; longer presets are hidden/)).toBeInTheDocument();
});

test("a custom duration parses Go syntax and echoes it back", async () => {
  const saved = render();
  await openDelivery();
  await userEvent.click(radio("Idle timeout", "custom…"));
  const input = screen.getByLabelText("Idle timeout, custom");
  await userEvent.type(input, "1m30s");
  expect(screen.getByText("= 90 s")).toBeInTheDocument();
  await userEvent.clear(input);
  await userEvent.type(input, "soon");
  await waitFor(() => expect(input).toHaveAccessibleDescription(/must be a duration/));
  await userEvent.clear(input);
  await userEvent.type(input, "45s");
  await submit();
  await waitFor(() => expect(saved.body?.["settings"]).toEqual({ idleTimeout: "45s" }));
});

test("a changed setting is marked custom, and resetting it clears the override", async () => {
  const hook = { ...fixtures.hook, settings: { holdTimeout: "2m0s" } };
  const saved = render(fixtures.me, hook);
  expect(radio("Hold timeout", "2 min")).toHaveAttribute("aria-checked", "true");
  expect(screen.getByText("1 setting differs from the relay defaults")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Reset hold timeout to 30 s" }));
  expect(radio("Hold timeout", /30 s/)).toHaveAttribute("aria-checked", "true");
  expect(screen.queryByText("custom")).not.toBeInTheDocument();
  await submit();
  await waitFor(() => expect(saved.body?.["settings"]).toMatchObject({ holdTimeout: null }));
});

test("picking a default radio card resets; picking another overrides", async () => {
  const saved = render();
  await openDelivery();
  expect(radio("Attach policy", /Take over/)).toHaveAttribute("aria-checked", "true");
  await userEvent.click(radio("Attach policy", /Reject/));
  expect(screen.getByText("custom")).toBeInTheDocument();
  await userEvent.click(radio("Attach policy", /Take over/));
  expect(screen.queryByText("custom")).not.toBeInTheDocument();
  await userEvent.click(radio("Host header", /Its own host/));
  await userEvent.click(radio("Capture", "Off"));
  await submit();
  await waitFor(() =>
    expect(saved.body?.["settings"]).toEqual({ hostHeader: "rewrite", capture: false }),
  );
});

test("body size takes a unit, and rate limit fills in the other half from the default", async () => {
  const saved = render();
  await openDelivery();
  await userEvent.type(screen.getByLabelText("Size"), "10");
  await userEvent.type(screen.getByLabelText("per second"), "20");
  expect(screen.getByLabelText("per second")).toHaveAccessibleName("Rate limit per second");
  expect(screen.getByRole("button", { name: "unlimited" })).toHaveAttribute(
    "aria-pressed",
    "false",
  );
  await submit();
  await waitFor(() =>
    expect(saved.body?.["settings"]).toEqual({
      maxBodyBytes: 10 << 20,
      rateLimit: { perSecond: 20, burst: 200 },
    }),
  );
});

test("clearing the rate limit's halves goes back to the default", async () => {
  const saved = render(fixtures.me, {
    ...fixtures.hook,
    settings: { rateLimit: { perSecond: 5, burst: 9 } },
  });
  await userEvent.clear(screen.getByLabelText("per second"));
  await userEvent.clear(screen.getByLabelText("burst"));
  expect(screen.getByLabelText("per second")).toHaveValue(null);
  expect(screen.queryByText("custom")).not.toBeInTheDocument();
  await submit();
  await waitFor(() => expect(saved.body?.["settings"]).toMatchObject({ rateLimit: null }));
});

test("the channel says which pattern allows it, and flags one no pattern allows", async () => {
  server.use(
    http.get("/channels", () =>
      HttpResponse.json({
        receive: ["default", "prod", "staging.*"],
        send: [],
        channels: [
          { name: "default", hookCount: 1, receive: ["default"] },
          { name: "staging.eu", hookCount: 1, receive: ["staging.*"] },
        ],
      }),
    ),
  );
  render();
  const channel = screen.getByRole("combobox", { name: "Channel" });
  expect(channel).toHaveAccessibleDescription(/allowed by default/);
  await userEvent.click(channel);
  const listbox = await screen.findByRole("listbox");
  await waitFor(() =>
    expect(
      within(listbox)
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual(["default", "prod", "staging.eu"]),
  );
  await userEvent.type(channel, "staging.us");
  await waitFor(() => expect(channel).toHaveAccessibleDescription(/allowed by staging\.\*/));
  await userEvent.clear(channel);
  await userEvent.type(channel, "team-x");
  await waitFor(() =>
    expect(channel).toHaveAccessibleDescription(/none of your receive patterns allows it/),
  );
  await userEvent.clear(channel);
  await userEvent.type(channel, " prod ");
  await waitFor(() => expect(channel).toHaveAccessibleDescription(/allowed by prod/));
  await userEvent.clear(channel);
  await userEvent.type(channel, "Team X");
  await waitFor(() => expect(channel).toHaveAccessibleDescription(/dot-separated segments/));
  expect(channel).toHaveAttribute("aria-invalid", "true");
});

test("expiry quick picks stay within maxHookTtl, which also takes away never", async () => {
  const me = { ...fixtures.me, limits: { ...fixtures.me.limits, maxHookTtl: "168h0m0s" } };
  const saved = render(me);
  const picks = () =>
    screen.getAllByRole("button", { name: /^(Expire in|Never)/ }).map((b) => b.textContent);
  expect(picks()).toEqual(["1 h", "1 d", "7 d"]);
  expect(screen.getByText("never expires · your grants allow at most 7 d")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Expire in 1 d" }));
  expect(screen.getByText("in 1 d · your grants allow at most 7 d")).toBeInTheDocument();
  const at = Date.parse((screen.getByLabelText("Expires at") as HTMLInputElement).value);
  expect(at - Date.now()).toBeGreaterThan(23.9 * 3_600_000);
  // The limit is for persistent hooks.
  await userEvent.click(radio("Kind", "Ephemeral"));
  expect(picks()).toEqual(["1 h", "1 d", "7 d", "30 d", "never"]);
  await userEvent.click(screen.getByRole("button", { name: "Never expire" }));
  await submit();
  await waitFor(() => expect(saved.body).toEqual({ ephemeral: true, public: false }));
});

const tz = process.env["TZ"];
afterEach(() => {
  process.env["TZ"] = tz;
});

test("expiry is shown in local time and stored in UTC", async () => {
  process.env["TZ"] = "Pacific/Auckland"; // UTC+13 in December
  const saved = render();
  fireEvent.change(screen.getByLabelText("Expires at"), { target: { value: "2026-12-31T18:00" } });
  await submit();
  await waitFor(() => expect(saved.body?.["expiresAt"]).toBe("2026-12-31T05:00:00Z"));
});

test("an existing expiry is shown in local time", () => {
  process.env["TZ"] = "Pacific/Auckland";
  render(fixtures.me, { ...fixtures.hook, expiresAt: "2026-12-31T05:00:00Z" });
  expect(screen.getByLabelText("Expires at")).toHaveValue("2026-12-31T18:00");
});

test("the preview shows every effective setting, marking the defaults, and follows the dry run", async () => {
  render();
  const preview = screen.getByRole("complementary", { name: "Preview" });
  expect(preview).toHaveTextContent("three random words, chosen on save");
  expect(preview).toHaveTextContent("Hold30 s default");
  expect(preview).toHaveTextContent("Rate100/s, burst 200 default");
  const capture = fixtures.me.policy.defaults.capture ? "on" : "off";
  expect(preview).toHaveTextContent(`Capture${capture} default`);
  await userEvent.type(screen.getByLabelText("Path"), "idle");
  // The form's own idea of the URL, until the dry run answers with the relay's.
  expect(preview).toHaveTextContent(`${window.location.origin}/~dev/idle`);
  await waitFor(() => expect(preview).toHaveTextContent(fixtures.idle.url));
  await openDelivery();
  await userEvent.click(radio("Hold timeout", "2 min"));
  expect(preview).toHaveTextContent(/Hold2 min(?! default)/);
  expect(preview).toHaveTextContent("Channeldefault via default");
  expect(preview).toHaveTextContent("Lifetimepersistent, no expiry");
});

test("each invalid field shows its error, though neither changes the request", async () => {
  render();
  await openDelivery();
  await userEvent.click(radio("Idle timeout", "custom…"));
  const idle = screen.getByLabelText("Idle timeout, custom");
  await userEvent.type(idle, "soon");
  await waitFor(() => expect(idle).toHaveAccessibleDescription(/must be a duration/));
  await userEvent.click(radio("Response timeout", "custom…"));
  const response = screen.getByLabelText("Response timeout, custom");
  await userEvent.type(response, "later");
  await waitFor(() => expect(response).toHaveAccessibleDescription(/must be a duration/));
  expect(idle).toHaveAccessibleDescription(/must be a duration/);
});
