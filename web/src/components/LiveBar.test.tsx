import { QueryClient } from "@tanstack/react-query";
import { act, screen } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { expect, test } from "vitest";
import { LiveProvider } from "@/lib/live";
import { FakeEventSource } from "@/test/eventSource";
import { renderWithProviders } from "@/test/render";
import { server } from "@/test/server";
import { LiveBar } from "./LiveBar";

test("shows while the stream is reconnecting", async () => {
  const client = new QueryClient();
  renderWithProviders(
    <LiveProvider client={client} user>
      <LiveBar />
    </LiveProvider>,
    { client },
  );
  expect(screen.queryByText(/Live updates paused/)).not.toBeInTheDocument();
  act(() => FakeEventSource.latest()!.open());
  act(() => FakeEventSource.latest()!.fail());
  expect(await screen.findByText(/Live updates paused, reconnecting/)).toBeInTheDocument();
});

const failWith = (status: number, detail: string) => {
  server.use(http.get("/hooks", () => HttpResponse.json({ status, detail }, { status })));
  const client = new QueryClient();
  renderWithProviders(
    <LiveProvider client={client} user>
      <LiveBar />
    </LiveProvider>,
    { client },
  );
  act(() => FakeEventSource.latest()!.fail());
};

test("says when this device has too many streams open", async () => {
  failWith(429, "this node has too many event streams open");
  expect(
    await screen.findByText(/too many live connections from this device/i),
  ).toBeInTheDocument();
  expect(screen.getByText(/close other tsrelay tabs/i)).toBeInTheDocument();
});

test("says why when the relay refuses the stream", async () => {
  failWith(403, "event streams need a relay role");
  expect(
    await screen.findByText("Live updates are off: event streams need a relay role."),
  ).toBeInTheDocument();
});
