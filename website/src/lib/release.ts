import { repoSlug } from "./navigation";

let latest: Promise<string> | undefined;

/**
 * The tag of the latest published release, as GitHub names it: drafts and
 * pre-releases never are, so the site cannot offer a version whose release
 * is still being built, or one whose release failed. Asked once per build.
 *
 * In CI the workflow's token is sent along, to stay clear of the API's limit
 * for anonymous requests. Every page waits on this, so a request that hangs
 * is cut off rather than left to stall the build until the job times out.
 */
export function latestVersion(): Promise<string> {
  latest ??= fetchLatestVersion();

  return latest;
}

async function fetchLatestVersion(): Promise<string> {
  const token = process.env.GITHUB_TOKEN;
  const response = await fetch(`https://api.github.com/repos/${repoSlug}/releases/latest`, {
    headers: {
      Accept: "application/vnd.github+json",
      ...(token && { Authorization: `Bearer ${token}` }),
    },
    signal: AbortSignal.timeout(15_000),
  });

  if (!response.ok) {
    throw new Error(`could not read the latest release: GitHub answered ${response.status}`);
  }

  const { tag_name: tag } = (await response.json()) as { tag_name: string };

  return tag;
}
