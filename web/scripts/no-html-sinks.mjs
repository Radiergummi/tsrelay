// Fails if anything under src/ uses an HTML sink. Captured data is only
// ever rendered as React text (DESIGN.md §10.2), so none of these has a
// legitimate use in this app, tests included.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";

const sinks = /dangerouslySetInnerHTML|innerHTML|outerHTML|insertAdjacentHTML|document\.write/;
const root = new URL("../src/", import.meta.url).pathname;
const found = [];
const walk = (dir) => {
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) walk(path);
    else if (/\.(ts|tsx|js|jsx|mjs)$/.test(name)) {
      readFileSync(path, "utf8")
        .split("\n")
        .forEach((line, i) => {
          if (sinks.test(line)) found.push(`${path.slice(root.length)}:${i + 1}: ${line.trim()}`);
        });
    }
  }
};
walk(root);
if (found.length) {
  console.error("HTML sinks are not allowed in web/src:\n" + found.join("\n"));
  process.exit(1);
}
