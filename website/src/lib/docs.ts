import { getCollection } from "astro:content";
import { execFileSync } from "node:child_process";
import { resolve } from "node:path";

export const docsDir = resolve("../docs");

/**
 * The most recent released version: the highest `v*` tag that is not a
 * pre-release, so the site cannot fall behind a release or offer an
 * `-rc` as the download. Tags are what the release workflow publishes from,
 * so they are the one record of what has shipped.
 */
export function latestVersion(): string {
  const tags = execFileSync("git", ["tag", "--list", "v*", "--sort=-v:refname"], {
    encoding: "utf-8",
    timeout: 5000,
  });
  const version = tags.split("\n").find((tag) => /^v\d+\.\d+\.\d+$/.test(tag));

  if (!version) {
    throw new Error("no release tag in this clone; check out with fetch-depth: 0 and tags");
  }

  return version;
}

export async function getDocPaths() {
  const docs = await getCollection("docs");

  return docs.filter(({ data: { category } }) => category !== "overview");
}
