import { createHash } from "node:crypto";
import { readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { gzipSync, zstdCompressSync } from "node:zlib";
import type { Plugin } from "vite";

/** Files smaller than this are not worth a compressed copy. */
const threshold = 1024;

/** Formats that are compressed already. */
const precompressed = new Set(["woff2", "woff", "png", "jpg", "webp", "avif", "ico"]);

export interface ManifestEntry {
  etag: string;
  variants: Partial<Record<"gzip" | "zstd", { etag: string }>>;
}

/**
 * etagOf mirrors internal/ui's etagOf: the first 16 bytes of the SHA-256,
 * in hex, without quotes (the Go handler adds them).
 */
function etagOf(data: Buffer): string {
  return createHash("sha256").update(data).digest().subarray(0, 16).toString("hex");
}

/** files lists dir's files below root, skipping hidden ones and our own output. */
function files(root: string, dir = root): string[] {
  return readdirSync(dir).flatMap((name) => {
    if (name.startsWith(".")) return [];
    const path = join(dir, name);
    if (statSync(path).isDirectory()) return files(root, path);
    const rel = relative(root, path).split("\\").join("/");
    if (rel === "index.html" || rel === "assets-manifest.json" || /\.(gz|zst)$/.test(rel))
      return [];
    return [rel];
  });
}

/**
 * writeManifest writes gzip and zstd copies of the build's larger files and
 * assets-manifest.json, which internal/ui reads for ETags and variants. Go
 * serves index.html itself. It writes dist/.gitkeep back, because vite
 * build empties the directory and the Go embed needs a file there.
 */
export function writeManifest(dir: string): Record<string, ManifestEntry> {
  const manifest: Record<string, ManifestEntry> = {};
  for (const name of files(dir)) {
    const path = join(dir, name);
    const data = readFileSync(path);
    const entry: ManifestEntry = { etag: etagOf(data), variants: {} };
    const ext = name.slice(name.lastIndexOf(".") + 1).toLowerCase();
    if (data.byteLength >= threshold && !precompressed.has(ext)) {
      const gz = gzipSync(data, { level: 9 });
      const zst = zstdCompressSync(data);
      writeFileSync(`${path}.gz`, gz);
      writeFileSync(`${path}.zst`, zst);
      entry.variants = { gzip: { etag: etagOf(gz) }, zstd: { etag: etagOf(zst) } };
    }
    manifest[name] = entry;
  }
  writeFileSync(join(dir, "assets-manifest.json"), JSON.stringify(manifest, null, 2));
  writeFileSync(join(dir, ".gitkeep"), "");
  return manifest;
}

/**
 * precompress runs writeManifest once the build is written, public files
 * (theme.js, the favicon) included.
 */
export function precompress(): Plugin {
  let outDir = "";
  return {
    name: "tsrelay:precompress",
    apply: "build",
    configResolved(config) {
      outDir = resolve(config.root, config.build.outDir);
    },
    closeBundle() {
      writeManifest(outDir);
    },
  };
}
