import { QueryClient } from "@tanstack/react-query";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { useState } from "react";
import { expect, test, vi } from "vitest";
import { fixtures } from "@/test/fixtures";
import { renderWithProviders } from "@/test/render";
import { server } from "@/test/server";
import { Inspector } from "./Inspector";

const show = (callId: string, me = fixtures.me, hook = fixtures.hook) => {
  const onSelect = vi.fn<(callId: string) => void>();
  renderWithProviders(<Inspector hook={hook} me={me} callId={callId} onSelect={onSelect} />);
  return onSelect;
};

test("the owner sees the request, the response and the actions", async () => {
  show("c_json");
  expect(
    await screen.findByRole("heading", { name: /POST \/payments\/done\?order=42/ }),
  ).toBeInTheDocument();
  expect(screen.getByText("200 OK")).toBeInTheDocument();
  expect(screen.getByText("Content-Type")).toBeInTheDocument();
  expect(screen.getByText(/left out/)).toBeInTheDocument(); // Authorization was redacted
  expect(screen.getByRole("button", { name: "Replay" })).toBeEnabled();
  expect(screen.getByRole("button", { name: /Copy as curl/ })).toBeEnabled();
  await userEvent.click(screen.getByRole("tab", { name: "Response" }));
  expect(await screen.findByText(/"received"/)).toBeInTheDocument();
  await userEvent.click(screen.getByRole("tab", { name: "Attempts" }));
  expect(screen.getByText("dev-laptop")).toBeInTheDocument();
  expect(screen.queryByRole("tab", { name: "Diff" })).not.toBeInTheDocument();
});

test("replay is disabled with the server's reason", async () => {
  show("c_cut");
  const replay = await screen.findByRole("button", { name: "Replay" });
  expect(replay).toBeDisabled();
  expect(screen.getByText(/request body was not captured in full/)).toBeInTheDocument();
});

test("replaying selects the new call", async () => {
  server.use(
    http.post("/api/v1/hooks/:id/calls/:callId/replay", () =>
      HttpResponse.json(fixtures.callReplay, { status: 201 }),
    ),
  );
  const onSelect = show("c_json");
  await userEvent.click(await screen.findByRole("button", { name: "Replay" }));
  await waitFor(() => expect(onSelect).toHaveBeenCalledWith("c_replay"));
  expect(await screen.findByText(/Replayed: 201 Created/)).toBeInTheDocument();
});

// The POST answer seeds the new call's cache entry, so it has to say whether
// the new call can be replayed; nothing refetches it while it is fresh (the
// app's staleTime), and the GET for it would 404 here.
test("after replaying, the new call can be replayed and copied at once", async () => {
  server.use(
    http.post("/api/v1/hooks/:id/calls/:callId/replay", () =>
      HttpResponse.json({ ...fixtures.callReplay, id: "c_new" }, { status: 201 }),
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 10_000 }, mutations: { retry: false } },
  });
  function Selecting() {
    const [callId, setCallId] = useState("c_json");
    return <Inspector hook={fixtures.hook} me={fixtures.me} callId={callId} onSelect={setCallId} />;
  }
  renderWithProviders(<Selecting />, { client });
  await userEvent.click(await screen.findByRole("button", { name: "Replay" }));
  expect(
    await screen.findByRole("heading", { name: /POST \/payments\/done\?order=42/ }),
  ).toBeInTheDocument();
  await waitFor(() => expect(screen.getByText("c_json")).toBeInTheDocument()); // "replay of c_json"
  expect(screen.getByRole("button", { name: "Replay" })).toBeEnabled();
  expect(screen.getByRole("button", { name: /Copy as curl/ })).toBeEnabled();
});

// user-event's setup() installs a clipboard stub, which readText reads back.
test("copy as curl writes the local-target command", async () => {
  const user = userEvent.setup();
  show("c_json");
  await user.click(await screen.findByRole("button", { name: "Copy as curl" }));
  expect(await navigator.clipboard.readText()).toMatch(
    /^curl -g -X 'POST' 'http:\/\/127\.0\.0\.1:\d+\/payments\/done\?order=42'/,
  );
});

test("an admin who doesn't own the hook sees metadata only, without actions", async () => {
  server.use(
    http.get("/api/v1/hooks/:id/calls/:callId", () => HttpResponse.json(fixtures.callAdmin)),
  );
  show("c_json", fixtures.meAdmin);
  expect(
    await screen.findByText(/only the hook's owner sees its query, headers and bodies/),
  ).toBeInTheDocument();
  expect(
    screen.getByText(/body, not shown: only the hook's owner sees bodies/),
  ).toBeInTheDocument();
  expect(screen.queryByText("No body.")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Replay" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /Copy as curl/ })).not.toBeInTheDocument();
});

test("a replay can be compared with its original", async () => {
  show("c_replay");
  await userEvent.click(await screen.findByRole("tab", { name: "Diff" }));
  expect(await screen.findByText(/201 Created/, { selector: "[data-op=add]" })).toBeInTheDocument();
  expect(screen.getByText(/200 OK/, { selector: "[data-op=del]" })).toBeInTheDocument();
});

test("an expired call says so", async () => {
  show("c_gone");
  expect(await screen.findByText("This call has expired from the inspector")).toBeInTheDocument();
});

test("r replays and c copies", async () => {
  const user = userEvent.setup();
  let replays = 0;
  server.use(
    http.post("/api/v1/hooks/:id/calls/:callId/replay", () => {
      replays++;
      return HttpResponse.json(fixtures.callReplay, { status: 201 });
    }),
  );
  show("c_json");
  await screen.findByRole("button", { name: "Replay" });
  await user.keyboard("c");
  expect(await navigator.clipboard.readText()).toMatch(/^curl /);
  await user.keyboard("r");
  await waitFor(() => expect(replays).toBe(1));
});
