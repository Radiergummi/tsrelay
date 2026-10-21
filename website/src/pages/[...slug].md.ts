import { readFile } from "node:fs/promises";
import type { APIContext } from "astro";
import { getDocPaths } from "../lib/docs";
import { markdownResponse } from "../lib/markdown";

export async function getStaticPaths() {
  const docs = await getDocPaths();

  return docs.map(({ filePath, id: slug }) => ({
    params: { slug },
    props: { filePath },
  }));
}

export async function GET({ params, props, site }: APIContext) {
  return markdownResponse(await readFile(props.filePath, "utf-8"), `/${params.slug}`, site);
}
