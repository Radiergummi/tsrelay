import type { RouteObject } from "react-router";
import { Layout } from "@/components/Layout";

// Pages are lazy, so the first load carries only the shell (the bundle
// budget is 120 KiB gzip of initial JavaScript).
export const routes: RouteObject[] = [
  {
    element: <Layout />,
    children: [
      {
        index: true,
        lazy: async () => ({ Component: (await import("@/pages/HooksPage")).HooksPage }),
      },
      {
        path: "hooks/new",
        lazy: async () => ({ Component: (await import("@/pages/HookFormPage")).HookFormPage }),
      },
      {
        path: "hooks/:id/edit",
        lazy: async () => ({ Component: (await import("@/pages/HookFormPage")).HookFormPage }),
      },
      {
        // The call route is a child without its own component, so selecting
        // a call changes the URL without remounting the hook page.
        path: "hooks/:id",
        lazy: async () => ({ Component: (await import("@/pages/HookPage")).HookPage }),
        children: [{ path: "calls/:callId" }],
      },
      {
        path: "admin",
        lazy: async () => ({ Component: (await import("@/pages/AdminPage")).AdminPage }),
      },
      {
        path: "inspect/:callId",
        lazy: async () => ({ Component: (await import("@/pages/InspectPage")).InspectPage }),
      },
      {
        path: "*",
        lazy: async () => ({ Component: (await import("@/pages/NotFoundPage")).NotFoundPage }),
      },
    ],
  },
];
