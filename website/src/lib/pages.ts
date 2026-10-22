import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { getLastModified, isShallow } from "./git";

/**
 * Where the docs live. Declared here rather than beside the content collection
 * because `astro.config.ts` imports this module before the content layer exists.
 */
export const docsDir = resolve("../docs");

/**
 * The home page's title and description, from the frontmatter of
 * `docs/index.md`. The page renders components rather than that file, but the
 * file is its Markdown representation, so the copy is written there once.
 */
const home = parse(readFileSync(resolve(docsDir, "index.md"), "utf-8").split(/^---$/m)[1]) as {
  title: string;
  description: string;
};

/**
 * A page this site serves that is not a doc in the content collection: the home
 * page, the API reference generated from the relay's OpenAPI document, and the
 * download page.
 *
 * The title and description are the page's own copy — the `.astro` pages read
 * them from here rather than declaring them a second time, so `llms.txt` and
 * `openapi.json` cannot describe a page differently from the page itself.
 */
export interface GeneratedPage {
  path: string;
  title: string;
  description: string;

  /**
   * The Markdown representation of this page, if it has one. `/api/explorer` has
   * none: the machine-readable form of a spec playground is the spec.
   */
  markdown?: string;

  /**
   * The file whose last commit dates this page, relative to the repository
   * root. For a page generated from another file that is the source it is
   * generated from, not the `.astro` file that renders it — a wording change to
   * the page's own prose is not what a reader means by "when did this last
   * change". A page written in its `.astro` file is dated by that file.
   */
  sourcePath: string;
}

export const generatedPages: GeneratedPage[] = [
  {
    path: "/",
    title: home.title,
    description: home.description,
    markdown: "/index.md",
    sourcePath: "docs/index.md",
  },
  {
    path: "/api/explorer",
    title: "API Reference",
    description: "Interactive reference for the relay's HTTP API.",
    sourcePath: "internal/api/openapi.yaml",
  },
  {
    path: "/download",
    title: "Download",
    description:
      "Download the tsrelay binary for macOS, Linux or Windows, or pull the container image, and verify what you downloaded.",
    sourcePath: "website/src/pages/download.astro",
  },
];

export function generatedPage(path: string): GeneratedPage {
  const page = generatedPages.find((candidate) => candidate.path === path);

  if (!page) {
    throw new Error(`no generated page for ${path}`);
  }

  return page;
}

/**
 * The source file behind a doc slug, relative to the repository root. It is
 * probed rather than read from the content collection because
 * `astro.config.ts` runs before the content layer exists and still needs to
 * date every page.
 */
function docsSourcePath(slug: string): string | null {
  return existsSync(resolve(docsDir, `${slug}.md`)) ? `docs/${slug}.md` : null;
}

/**
 * The source file behind a rendered path, relative to the repository root.
 */
function sourceForPath(pathname: string): string {
  const page = generatedPages.find((candidate) => candidate.path === pathname);

  if (page) {
    return page.sourcePath;
  }

  const source = docsSourcePath(pathname.replace(/^\//, ""));

  if (!source) {
    throw new Error(`no source file for ${pathname}; add it to generatedPages`);
  }

  return source;
}

/**
 * The `lastmod` for one sitemap entry. Throwing rather than omitting the date is
 * the point: a page added without a source mapping fails `astro build` instead
 * of shipping a sitemap entry that never appears to change. `sitemapRequired`
 * in `astro.config.ts` is what makes a throw here fail the build — the sitemap
 * integration itself catches and logs whatever `serialize` raises.
 */
export function lastModifiedFor(url: string): string {
  if (isShallow()) {
    throw new Error(
      "a shallow clone dates every page from its one commit; check out with fetch-depth: 0",
    );
  }

  const { pathname } = new URL(url);
  const source = sourceForPath(pathname === "/" ? "/" : pathname.replace(/\/$/, ""));
  const modified = getLastModified(source);

  if (!modified) {
    throw new Error(`no commit in this clone touches ${source}; commit it before building`);
  }

  return modified.toISOString();
}
