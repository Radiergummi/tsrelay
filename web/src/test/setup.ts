import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterAll, afterEach, beforeAll, beforeEach } from "vitest";

afterEach(() => cleanup());

// This file also runs for plugins/*.test.ts, which opt into the "node"
// environment and have no window or Element. ./server pulls in msw/node,
// which Node only needs for jsdom tests, so it's imported dynamically here
// rather than at the top: a node-environment test never constructs it and
// never prints Node's experimental-localStorage warning for it.
if (typeof window !== "undefined") {
  const { server } = await import("./server");
  beforeAll(() => server.listen({ onUnhandledFrame: "error" }));
  afterEach(() => server.resetHandlers());
  afterAll(() => server.close());

  const { FakeEventSource } = await import("./eventSource");
  (globalThis as { EventSource: unknown }).EventSource = FakeEventSource;
  beforeEach(() => {
    FakeEventSource.instances = [];
  });

  // jsdom has no matchMedia; tests that need a dark preference override it.
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }),
  });

  // jsdom implements no layout, so it has no scrollIntoView.
  Element.prototype.scrollIntoView = () => {};
}
