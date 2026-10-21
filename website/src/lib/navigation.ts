import packageJson from "../../package.json" with { type: "json" };

export const repoUrl: string = packageJson.repository;

export interface NavItem {
  slug: string;
  title: string;

  /**
   * Force full page reload when navigating to this page (skips View Transitions).
   */
  reload?: boolean;
}

export interface NavGroup {
  label: string;
  items: NavItem[];
}

const allPages: NavItem[] = [];

export function getPrevNext(slug: string): { prev: NavItem | null; next: NavItem | null } {
  if (!allPages.length) {
    for (const { items } of sidebarGroups) {
      allPages.push(...items);
    }
  }

  const index = allPages.findIndex((item) => item.slug === slug);

  return {
    prev: index > 0 ? allPages[index - 1] : null,
    next: index >= 0 && index < allPages.length - 1 ? allPages[index + 1] : null,
  };
}

export const sidebarGroups: NavGroup[] = [
  {
    label: "Using tsrelay",
    items: [
      { slug: "getting-started", title: "Getting Started" },
      { slug: "hooks", title: "Hooks" },
      { slug: "background-tunnels", title: "Background Tunnels" },
      { slug: "receiving-calls", title: "Receiving Calls" },
      { slug: "web-ui", title: "Web UI and Inspector" },
      { slug: "api", title: "API" },
      { slug: "cli", title: "Command Line" },
    ],
  },
  {
    label: "Running a relay",
    items: [
      { slug: "running-a-relay", title: "Running a Relay" },
      { slug: "access-control", title: "Access Control" },
      { slug: "observability", title: "Observability" },
    ],
  },
  {
    label: "Reference",
    items: [
      { slug: "configuration", title: "Configuration" },
      { slug: "errors", title: "Errors" },
      { slug: "api/explorer", title: "API Reference", reload: true },
      { slug: "download", title: "Download" },
    ],
  },
];
