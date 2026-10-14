// Checks the build against the bundle budget: the JavaScript the first
// page load needs (the entry chunk and its static imports) and all CSS,
// gzipped. Run after `pnpm build`.
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { gzipSync } from "node:zlib";

const KiB = 1024;
const budget = { js: 120 * KiB, css: 20 * KiB };
const dist = new URL("../dist/", import.meta.url).pathname;
const manifest = JSON.parse(readFileSync(join(dist, ".vite/manifest.json"), "utf8"));
const gz = (file) => gzipSync(readFileSync(join(dist, file)), { level: 9 }).byteLength;

const initial = new Set();
const visit = (key) => {
  const chunk = manifest[key];
  if (!chunk || initial.has(chunk.file)) return;
  initial.add(chunk.file);
  for (const imp of chunk.imports ?? []) visit(imp);
};
for (const [key, chunk] of Object.entries(manifest)) if (chunk.isEntry) visit(key);

const css = readdirSync(join(dist, "assets"))
  .filter((f) => f.endsWith(".css"))
  .map((f) => `assets/${f}`);
const js = [...initial].filter((f) => f.endsWith(".js"));
const total = (list) => list.reduce((sum, f) => sum + gz(f), 0);
const result = { js: total(js), css: total(css) };

let failed = false;
for (const kind of ["js", "css"]) {
  const over = result[kind] > budget[kind];
  failed ||= over;
  console.log(
    `${over ? "OVER" : "ok  "} ${kind}: ${(result[kind] / KiB).toFixed(1)} KiB gzip of ${budget[kind] / KiB} KiB`,
  );
}
for (const f of js) console.log(`     ${f}: ${(gz(f) / KiB).toFixed(1)} KiB`);
process.exit(failed ? 1 : 0);
