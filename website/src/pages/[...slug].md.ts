import { readFile } from "node:fs/promises";
import { join } from "node:path";
import type { APIContext } from "astro";
import { docsDir, getDocPaths } from "../lib/docs";
import { markdownResponse } from "../lib/markdown";

export async function getStaticPaths() {
  const docs = await getDocPaths();

  return docs.map(({ filePath, id: slug }) => ({
    params: { slug },
    props: { filePath },
  }));
}

export async function GET({ params, props, site }: APIContext) {
  const path: string = props.filePath ?? join(docsDir, "not-found");

  let content: string;

  try {
    content = await readFile(path, "utf-8");
  } catch {
    return new Response("Not found", { status: 404 });
  }

  return markdownResponse(content, `/${params.slug}`, site);
}
