import { QueryClient } from "@tanstack/react-query";
import { act, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { LiveProvider } from "@/lib/live";
import { FakeEventSource } from "@/test/eventSource";
import { renderWithProviders } from "@/test/render";
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
