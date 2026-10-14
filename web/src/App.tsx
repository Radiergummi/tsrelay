import { QueryClientProvider } from "@tanstack/react-query";
import { lazy, Suspense, useState } from "react";
import { createBrowserRouter, RouterProvider } from "react-router";
import { makeQueryClient } from "@/api/queryClient";
import { routes } from "@/routes";

// The toaster is loaded lazily, as a sibling of the router rather than a
// wrapper around it, so its Base UI toast runtime doesn't hold up the first
// paint (the bundle budget is 120 KiB gzip of initial JavaScript).
const Toaster = lazy(() => import("@/components/ui/toast").then((m) => ({ default: m.Toaster })));

export function App() {
  const [client] = useState(makeQueryClient);
  const [router] = useState(() => createBrowserRouter(routes));
  return (
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
      <Suspense>
        <Toaster />
      </Suspense>
    </QueryClientProvider>
  );
}
