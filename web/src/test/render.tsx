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
  {
    route = "/",
    path = "/",
    client = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    }),
    context,
  }: { route?: string; path?: string; client?: QueryClient; context?: unknown } = {},
) {
  const leaf = { path, element: ui };
  const tree =
    context === undefined ? [leaf] : [{ element: <Outlet context={context} />, children: [leaf] }];
  const router = createMemoryRouter([...tree, { path: "*", element: <p>elsewhere</p> }], {
    initialEntries: [route],
  });
  const result = render(
    <QueryClientProvider client={client}>
      <Toaster />
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return { ...result, client, router };
}
