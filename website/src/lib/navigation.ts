import packageJson from "../../package.json" with { type: "json" };

export const repoUrl: string = packageJson.repository;

/** The repository as `owner/name`, as `gh -R` and the container registry name it. */
export const repoSlug = new URL(repoUrl).pathname.slice(1);

export function releaseUrl(version: string): string {
  return `${repoUrl}/releases/tag/${version}`;
}

export interface NavItem {
  slug: string;
  title: string;
}

export interface NavGroup {
  label: string;
  items: NavItem[];
}

export const sidebarGroups: NavGroup[] = [
  {
    label: "Using tsrelay",
    items: [
      { slug: "getting-started", title: "Getting started" },
      { slug: "hooks", title: "Hooks" },
      { slug: "background-tunnels", title: "Background tunnels" },
      { slug: "receiving-calls", title: "Receiving calls" },
      { slug: "web-ui", title: "Web UI and inspector" },
      { slug: "api", title: "API" },
      { slug: "cli", title: "Command line" },
    ],
  },
  {
    label: "Running a relay",
    items: [
      { slug: "running-a-relay", title: "Running a relay" },
      { slug: "access-control", title: "Access control" },
      { slug: "observability", title: "Observability" },
    ],
  },
  {
    label: "Reference",
    items: [
      { slug: "configuration", title: "Configuration" },
      { slug: "errors", title: "Errors" },
      { slug: "api/explorer", title: "API reference" },
      { slug: "download", title: "Download" },
    ],
  },
];

const allPages = sidebarGroups.flatMap(({ items }) => items);

export function getPrevNext(slug: string): { prev: NavItem | null; next: NavItem | null } {
  const index = allPages.findIndex((item) => item.slug === slug);

  return {
    prev: index > 0 ? allPages[index - 1] : null,
    next: index >= 0 && index < allPages.length - 1 ? allPages[index + 1] : null,
  };
}
