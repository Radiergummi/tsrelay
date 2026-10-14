import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

afterEach(() => cleanup());

// This file also runs for plugins/*.test.ts, which opt into the "node"
// environment and have no window or Element.
if (typeof window !== "undefined") {
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
