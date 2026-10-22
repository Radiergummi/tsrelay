import { execFileSync } from "node:child_process";

/**
 * Every git question the build asks has one answer per build, so each is asked
 * once. The calls are synchronous because the sitemap's `serialize` hook runs
 * inside `astro.config.ts`, which has no way to await.
 */
function gitSync(args: string[], cwd?: string): string {
  return execFileSync("git", args, { cwd, encoding: "utf-8", timeout: 5000 }).trim();
}

let repoRoot: string | undefined;

function getRepoRoot(): string {
  repoRoot ??= gitSync(["rev-parse", "--show-toplevel"]);

  return repoRoot;
}

const lastModified = new Map<string, Date | null>();

/**
 * The author date of the last commit that touched `relativePath` (relative to
 * the repository root), or null if no commit in this clone touches it.
 */
export function getLastModified(relativePath: string): Date | null {
  if (!lastModified.has(relativePath)) {
    const stdout = gitSync(["log", "-1", "--format=%aI", "--", relativePath], getRepoRoot());

    lastModified.set(relativePath, stdout ? new Date(stdout) : null);
  }

  return lastModified.get(relativePath) ?? null;
}

let shallow: boolean | undefined;

/**
 * Whether this is a shallow clone.
 *
 * Worth asking separately because a shallow clone does not fail the date lookup
 * — it corrupts it silently. The clone holds a single commit with no parent, so
 * git reports every path in the tree as added by it and `git log -1 -- <path>`
 * hands back the tip commit's date for *every* file rather than nothing. The
 * sitemap would then publish one date, the day of the deploy, as the `lastmod`
 * of every page, and no missing-date check would ever notice.
 */
export function isShallow(): boolean {
  shallow ??= gitSync(["rev-parse", "--is-shallow-repository"]) === "true";

  return shallow;
}
