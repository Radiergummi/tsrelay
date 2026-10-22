import { getCollection } from "astro:content";
import { sidebarGroups } from "./navigation";
import { generatedPage } from "./pages";

export interface SitePage {
  path: string;
  title: string;
  description: string;
  markdown?: string;
}

export interface SiteSection {
  label: string;
  pages: SitePage[];
}

/**
 * Every documentation page, grouped and ordered the way the sidebar presents
 * them, with the home page ahead of the rest.
 *
 * The sidebar is the order a reader is offered, so it is the order an agent is
 * offered too. A sidebar entry that is neither a doc in the collection nor a
 * page in `generatedPages` throws, so a page cannot be added to the navigation
 * and silently left out of `llms.txt` and `openapi.json`.
 */
export async function siteSections(): Promise<SiteSection[]> {
  const docs = await getCollection("docs");
  const bySlug = new Map(docs.map((doc) => [doc.id, doc]));

  const sections = sidebarGroups.map(({ items, label }) => ({
    label,
    pages: items.map(({ slug }): SitePage => {
      const doc = bySlug.get(slug);

      if (!doc) {
        return generatedPage(`/${slug}`);
      }

      return {
        path: `/${slug}`,
        title: doc.data.title,
        description: doc.data.description,
        markdown: `/${slug}.md`,
      };
    }),
  }));

  sections[0].pages.unshift(generatedPage("/"));

  return sections;
}

/**
 * Every page this site serves, in the order it presents them. `openapi.json`
 * enumerates this; `llms.txt` renders the same pages grouped.
 */
export async function allPages(): Promise<SitePage[]> {
  const sections = await siteSections();

  return sections.flatMap(({ pages }) => pages);
}

/**
 * One machine-readable resource this site serves.
 */
export interface SiteResource {
  path: string;
  mediaType: string;
  title: string;
  description: string;
}

/**
 * The resources that are not pages: specs and the crawler files.
 *
 * `/api/openapi.yaml` is the API of the relay being documented; the site's own
 * description lives at the root, in both formats, so an agent probing either
 * `/openapi.json` or `/openapi.yaml` learns what this origin actually serves
 * rather than reading the relay's endpoints as though they were here.
 */
export const siteResources: SiteResource[] = [
  {
    path: "/openapi.json",
    mediaType: "application/json",
    title: "This site's OpenAPI description",
    description: "Every URL this documentation site serves, and what it responds with.",
  },
  {
    path: "/openapi.yaml",
    mediaType: "application/yaml",
    title: "This site's OpenAPI description, as YAML",
    description: "The same description as /openapi.json.",
  },
  {
    path: "/api/openapi.yaml",
    mediaType: "application/yaml",
    title: "tsrelay API specification",
    description:
      "The OpenAPI specification of the relay's HTTP API (the product this site documents, not this site).",
  },
  {
    path: "/llms.txt",
    mediaType: "text/plain",
    title: "llms.txt",
    description: "This site's contents, as llmstxt.org describes.",
  },
  {
    path: "/sitemap-index.xml",
    mediaType: "application/xml",
    title: "Sitemap index",
    description:
      "Points at the sitemap files listing every page, each with its last-modified date.",
  },
  {
    path: "/robots.txt",
    mediaType: "text/plain",
    title: "robots.txt",
    description: "Crawl policy. Everything here is open to crawlers.",
  },
];
