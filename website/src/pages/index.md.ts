import { readFile } from "node:fs/promises";
import { join } from "node:path";
import type { APIContext } from "astro";
import { markdownResponse } from "@/lib/markdown";
import { docsDir } from "@/lib/pages";

/**
 * The home page as Markdown. `[...slug].md.ts` cannot serve it: it drops
 * `category: overview` from its routes, and loosening that would also mint a
 * duplicate `/index` HTML page beside `/`.
 */
export async function GET({ site }: APIContext) {
  const body = await readFile(join(docsDir, "index.md"), "utf-8");

  return markdownResponse(body, "/", site);
}
