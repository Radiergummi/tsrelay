// @vitest-environment node
/// <reference types="node" />
import { execFileSync } from "node:child_process";
import { expect, test } from "vitest";
import { decodeBody, type DecodedBody } from "./bytes";
import { curlCommand, shellQuote } from "./curl";

const text = (s: string): DecodedBody =>
  decodeBody({
    body: Buffer.from(s).toString("base64"),
    size: Buffer.byteLength(s),
    captured: true,
  });
const raw = (bytes: number[]): DecodedBody =>
  decodeBody({ body: Buffer.from(bytes).toString("base64"), size: bytes.length, captured: true });
const none = decodeBody({ size: 0, captured: true });

/** argv runs cmd with curl replaced by a function that prints its arguments, NUL-separated. */
function argv(cmd: string): string[] {
  const out = execFileSync("sh", [
    "-c",
    `curl() { for a in "$@"; do printf '%s\\0' "$a"; done; }; ${cmd}`,
  ]).toString();
  return out.replace(/\0$/, "").split("\0");
}

test("a shell reads a quoted string back unchanged", () => {
  for (const s of [
    "",
    "plain",
    "it's",
    "a\nb",
    "a\r\nb",
    "\r",
    "$(touch /tmp/x)",
    "`id` \\ \" '\"'",
    "ümlaut",
  ]) {
    expect(execFileSync("sh", ["-c", `printf %s ${shellQuote(s)}`]).toString()).toBe(s);
  }
});

test("a shell running the command passes curl exactly the captured request", () => {
  const body = '{"note": "it\'s $(id)"}\nline two';
  const cmd = curlCommand(
    "POST",
    "http://localhost:8080/done?a=1&b=2",
    {
      "Content-Type": ["application/json"],
      "Content-Length": ["42"], // curl sets its own
      "X-Note": ["it's"],
    },
    text(body),
  );
  expect(argv(cmd)).toEqual([
    "-g",
    "-X",
    "POST",
    "http://localhost:8080/done?a=1&b=2",
    "-H",
    "Content-Type: application/json",
    "-H",
    "X-Note: it's",
    "--data-binary",
    body,
  ]);
});

test("a captured Accept-Encoding makes curl decompress", () => {
  const cmd = curlCommand("GET", "http://localhost/done", { "Accept-Encoding": ["gzip"] }, none);
  expect(argv(cmd)).toEqual([
    "-g",
    "-X",
    "GET",
    "http://localhost/done",
    "--compressed",
    "-H",
    "Accept-Encoding: gzip",
  ]);
});

test("CR survives without a raw CR byte in the command", () => {
  const body = "--x\r\nContent-Disposition: form-data\r\n\r\nv\r\n--x--\r\n";
  const cmd = curlCommand(
    "POST",
    "http://localhost/done",
    { "Content-Type": ["multipart/form-data"] },
    text(body),
  );
  expect(cmd).not.toContain("\r");
  expect(argv(cmd).at(-1)).toBe(body);
});

test("bodies with NUL bytes or invalid UTF-8 reach curl's stdin byte for byte", () => {
  for (const bytes of [
    [0x61, 0x00, 0x62],
    [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0xff, 0x27, 0x5c, 0x25],
  ]) {
    const cmd = curlCommand(
      "POST",
      "http://localhost/up",
      { "Content-Type": ["application/octet-stream"] },
      raw(bytes),
    );
    expect(cmd).toContain("--data-binary @-");
    const stdin = execFileSync("sh", ["-c", `curl() { cat; }; ${cmd}`]);
    expect([...stdin]).toEqual(bytes);
  }
});
