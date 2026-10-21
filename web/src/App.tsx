import { QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import { createBrowserRouter, RouterProvider } from "react-router";
import { makeQueryClient } from "@/api/queryClient";
import { Toaster } from "@/components/ui/toast";
import { routes } from "@/routes";

export function App() {
  const [client] = useState(makeQueryClient);
  const [router] = useState(() => createBrowserRouter(routes));
  return (
    <QueryClientProvider client={client}>
      {/* First: it subscribes in an effect, and earlier siblings' effects run first. */}
      <Toaster />
      <RouterProvider router={router} />
    </QueryClientProvider>
  );
}
