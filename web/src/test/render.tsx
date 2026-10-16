import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import type { ReactElement } from "react";
import { createMemoryRouter, Outlet, RouterProvider } from "react-router";
import { Toaster } from "@/components/ui/toast";

/**
 * renderWithProviders renders ui at route (matched against path) with a
 * fresh query client that never retries. context stands in for the outlet
 * context the layout normally provides (e.g. the entry document), when ui reads it.
 */
export function renderWithProviders(
  ui: ReactElement,
  opts: { route?: string; path?: string; client?: QueryClient; context?: unknown } = {},
) {
  const client =
    opts.client ??
    new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const leaf = { path: opts.path ?? "/", element: ui };
  const tree =
    opts.context === undefined
      ? [leaf]
      : [{ element: <Outlet context={opts.context} />, children: [leaf] }];
  const router = createMemoryRouter([...tree, { path: "*", element: <p>elsewhere</p> }], {
    initialEntries: [opts.route ?? "/"],
  });
  const result = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
      <Toaster />
    </QueryClientProvider>,
  );
  return { ...result, client, router };
}
