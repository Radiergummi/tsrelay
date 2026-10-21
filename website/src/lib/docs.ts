import { getCollection } from "astro:content";

export async function getDocPaths() {
  const docs = await getCollection("docs");

  return docs.filter(({ data: { category } }) => category !== "overview");
}
