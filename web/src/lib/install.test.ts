import { expect, test } from "vitest";
import { detectPlatform, downloads, installCommand } from "./install";

test("detectPlatform picks Windows only for Windows browsers", () => {
  expect(detectPlatform("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")).toBe(
    "windows",
  );
  expect(
    detectPlatform("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15"),
  ).toBe("unix");
  expect(detectPlatform("Mozilla/5.0 (X11; Linux x86_64) Gecko/20100101 Firefox/131.0")).toBe(
    "unix",
  );
  expect(detectPlatform("Mozilla/5.0 (X11; CrOS x86_64 14541.0.0)")).toBe("unix");
});

test("installCommand runs the relay's script for the platform", () => {
  expect(installCommand("unix", "https://tsrelay.example.ts.net")).toBe(
    "curl -fsSL https://tsrelay.example.ts.net/install.sh | sh",
  );
  expect(installCommand("windows", "https://tsrelay.example.ts.net")).toBe(
    "irm https://tsrelay.example.ts.net/install.ps1 | iex",
  );
});

test("downloads cover the six release builds", () => {
  expect(downloads.map((d) => `${d.os}/${d.arch}`).sort()).toEqual([
    "darwin/amd64",
    "darwin/arm64",
    "linux/amd64",
    "linux/arm64",
    "windows/amd64",
    "windows/arm64",
  ]);
});
