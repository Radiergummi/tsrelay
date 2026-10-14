import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import type { ReactElement } from "react";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toaster } from "@/components/ui/toast";

/**
 * renderWithProviders renders ui at route (matched against path) with a
 * fresh query client that never retries.
 */
export function renderWithProviders(
  ui: ReactElement,
  opts: { route?: string; path?: string; client?: QueryClient } = {},
) {
  const client =
    opts.client ??
    new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const router = createMemoryRouter(
    [
      { path: opts.path ?? "/", element: ui },
      { path: "*", element: <p>elsewhere</p> },
    ],
    { initialEntries: [opts.route ?? "/"] },
  );
  const result = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
      <Toaster />
    </QueryClientProvider>,
  );
  return { ...result, client, router };
}
