/** matchChannel is authz.MatchChannel: "*" matches any run of characters. */
export function matchChannel(pattern: string, name: string): boolean {
  let p = 0;
  let n = 0;
  let star = -1;
  let mark = 0;
  while (n < name.length) {
    if (p < pattern.length && pattern[p] === "*") {
      star = p++;
      mark = n;
    } else if (p < pattern.length && pattern[p] === name[n]) {
      p++;
      n++;
    } else if (star >= 0) {
      p = star + 1;
      n = ++mark;
    } else {
      return false;
    }
  }
  while (p < pattern.length && pattern[p] === "*") p++;
  return p === pattern.length;
}

/** channelChoices are the channels a receiver can pick as they are: "default" if allowed, then each literal pattern. */
export function channelChoices(patterns: string[]): string[] {
  const out: string[] = [];
  if (patterns.some((p) => matchChannel(p, "default"))) out.push("default");
  for (const p of patterns) if (!p.includes("*") && !out.includes(p)) out.push(p);
  return out;
}
