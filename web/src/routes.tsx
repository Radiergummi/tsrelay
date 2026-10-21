import { Navigate, type RouteObject } from "react-router";
import { Layout } from "@/components/Layout";

// Pages are lazy, so the first load carries only the shell.
export const routes: RouteObject[] = [
  {
    element: <Layout />,
    children: [
      // The relay redirects / to /hooks itself; this covers links inside the app.
      { index: true, element: <Navigate to="/hooks" replace /> },
      {
        path: "hooks",
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
        children: [{ path: "calls" }, { path: "calls/:callId" }],
      },
      {
        path: "channels",
        lazy: async () => ({ Component: (await import("@/pages/ChannelsPage")).ChannelsPage }),
      },
      {
        path: "channels/:name",
        lazy: async () => ({ Component: (await import("@/pages/ChannelPage")).ChannelPage }),
      },
      {
        path: "cli",
        lazy: async () => ({ Component: (await import("@/pages/CliPage")).CliPage }),
      },
      {
        path: "admin",
        lazy: async () => ({ Component: (await import("@/pages/AdminPage")).AdminPage }),
      },
      {
        path: "licenses",
        lazy: async () => ({ Component: (await import("@/pages/LicensesPage")).LicensesPage }),
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
