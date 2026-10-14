// @vitest-environment node
import { createHash } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { gunzipSync, zstdDecompressSync } from "node:zlib";
import { expect, test } from "vitest";
import { writeManifest } from "./precompress";

const etag = (b: Buffer) => createHash("sha256").update(b).digest().subarray(0, 16).toString("hex");

test("writes variants for large files and an ETag for every served file", () => {
  const dir = mkdtempSync(join(tmpdir(), "precompress-"));
  mkdirSync(join(dir, "assets"));
  mkdirSync(join(dir, ".vite"));
  const big = Buffer.from("x".repeat(4096));
  writeFileSync(join(dir, "assets/index-abc.js"), big);
  writeFileSync(join(dir, "assets/inter-xyz.woff2"), Buffer.alloc(4096, 7));
  writeFileSync(join(dir, "theme.js"), "small");
  writeFileSync(join(dir, "index.html"), "<!doctype html>");
  writeFileSync(join(dir, ".vite/manifest.json"), "{}");

  const manifest = writeManifest(dir);

  expect(Object.keys(manifest).sort()).toEqual([
    "assets/index-abc.js",
    "assets/inter-xyz.woff2",
    "theme.js",
  ]);
  const js = manifest["assets/index-abc.js"]!;
  expect(js.etag).toBe(etag(big));
  expect(gunzipSync(readFileSync(join(dir, "assets/index-abc.js.gz")))).toEqual(big);
  expect(zstdDecompressSync(readFileSync(join(dir, "assets/index-abc.js.zst")))).toEqual(big);
  expect(js.variants.gzip?.etag).toBe(etag(readFileSync(join(dir, "assets/index-abc.js.gz"))));
  expect(manifest["theme.js"]!.variants).toEqual({});
  expect(manifest["assets/inter-xyz.woff2"]!.variants).toEqual({});
  expect(JSON.parse(readFileSync(join(dir, "assets-manifest.json"), "utf8"))).toEqual(manifest);
  expect(readFileSync(join(dir, ".gitkeep"), "utf8")).toBe("");
});
