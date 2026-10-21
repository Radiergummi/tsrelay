import type { APIContext } from "astro";
import { siteResources, siteSections } from "@/lib/manifest";
import { generatedPage } from "@/lib/pages";

/**
 * `llms.txt`, as https://llmstxt.org describes it: an H1, a blockquote summary,
 * optional prose, then H2 sections of `- [name](url): notes` links.
 *
 * Generated from the same manifest as `openapi.json`, in the sidebar's order, so
 * it cannot fall behind the navigation. Links point at the Markdown
 * representation of each page rather than the HTML: an agent following one gets
 * the page, not a shell of markup around it.
 */
export async function GET({ site }: APIContext) {
  const url = (path: string) => new URL(path, site).href;
  const sections = await siteSections();

  const lines = [
    "# tsrelay",
    "",
    `> ${generatedPage("/").description}`,
    "",
    "tsrelay is a single Go binary. Run as `tsrelay serve`, it is a relay node on a Tailscale tailnet that tailnet services send HTTP callbacks to; run as `tsrelay http <port>` on a developer's machine, it dials out to the relay and forwards those callbacks to a local app. Who may send and receive is decided by the tailnet policy.",
    "",
    "Every documentation page on this site also has a Markdown representation at the same path with `.md` appended, which is what the links below point at; the download page and the interactive API reference are HTML only. The site's own URLs are described by an OpenAPI document at " +
      url("/openapi.json") +
      "; the HTTP API of the relay itself is described separately at " +
      url("/api/openapi.yaml") +
      ".",
  ];

  for (const { label, pages } of sections) {
    lines.push("", `## ${label}`, "");

    for (const { description, markdown, path, title } of pages) {
      lines.push(`- [${title}](${url(markdown ?? path)}): ${description}`);
    }
  }

  lines.push("", "## Optional", "");

  for (const { description, path, title } of siteResources) {
    // Not this file. An agent reading it has already found it.
    if (path === "/llms.txt") {
      continue;
    }

    lines.push(`- [${title}](${url(path)}): ${description}`);
  }

  return new Response(lines.join("\n") + "\n", {
    headers: { "Content-Type": "text/plain; charset=utf-8" },
  });
}
