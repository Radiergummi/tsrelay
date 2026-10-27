/** matchChannel is authz.MatchChannel: "*" matches any run of characters. */
export function matchChannel(pattern: string, name: string): boolean {
  let patternAt = 0;
  let nameAt = 0;
  let star = -1;
  let mark = 0;
  while (nameAt < name.length) {
    if (patternAt < pattern.length && pattern[patternAt] === "*") {
      star = patternAt++;
      mark = nameAt;
    } else if (patternAt < pattern.length && pattern[patternAt] === name[nameAt]) {
      patternAt++;
      nameAt++;
    } else if (star >= 0) {
      patternAt = star + 1;
      nameAt = ++mark;
    } else {
      return false;
    }
  }
  while (patternAt < pattern.length && pattern[patternAt] === "*") patternAt++;
  return patternAt === pattern.length;
}

/** channelChoices are the channels a receiver can pick as they are: "default" if allowed, then each literal pattern. */
export function channelChoices(patterns: string[]): string[] {
  const out: string[] = [];
  if (patterns.some((pattern) => matchChannel(pattern, "default"))) out.push("default");
  for (const pattern of patterns)
    if (!pattern.includes("*") && !out.includes(pattern)) out.push(pattern);
  return out;
}

const channelRE = /^[a-z0-9-]+(\.[a-z0-9-]+)*$/;

/** validChannel is authz.ValidChannel: dot-separated segments of [a-z0-9-], at most 64 characters, not ending in .json or .html. */
export const validChannel = (name: string) =>
  name.length <= 64 && channelRE.test(name) && !/\.(json|html)$/.test(name);

/** allowingPattern is the pattern that lets a receiver use channel name: a literal match first, else the first wildcard that matches. */
export function allowingPattern(patterns: string[], name: string): string | undefined {
  return (
    patterns.find((pattern) => pattern === name) ??
    patterns.find((pattern) => matchChannel(pattern, name))
  );
}
