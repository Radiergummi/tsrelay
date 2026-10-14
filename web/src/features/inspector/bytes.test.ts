import { expect, test } from "vitest";
import { bodyKind, decodeBody, headerValue, trimPartialRune } from "./bytes";

const b64 = (bytes: number[]) => btoa(String.fromCharCode(...bytes));

test("decodes text, text with NUL, and binary", () => {
  const hello = btoa(String.fromCharCode(...new TextEncoder().encode("héllo")));
  expect(decodeBody({ body: hello, size: 6, captured: true })).toMatchObject({
    text: "héllo",
    binary: false,
  });
  expect(decodeBody({ body: b64([0x61, 0x00, 0x62]), size: 3, captured: true })).toMatchObject({
    text: "a\u0000b",
    hasNul: true,
    binary: true,
  });
  expect(decodeBody({ body: b64([0xff, 0xfe, 0x00]), size: 3, captured: true })).toMatchObject({
    text: null,
    binary: true,
  });
  expect(decodeBody({ size: 0, captured: true }).bytes.length).toBe(0);
});

test("a body cut inside a character is trimmed to the last whole one", () => {
  const euro = [0xe2, 0x82, 0xac];
  expect([...trimPartialRune(Uint8Array.from([0x61, ...euro.slice(0, 2)]))]).toEqual([0x61]);
  expect([...trimPartialRune(Uint8Array.from([0x61, ...euro]))]).toEqual([0x61, ...euro]);
  expect(
    decodeBody({ body: b64([0x61, 0xe2, 0x82]), size: 100, truncated: true, captured: true }),
  ).toMatchObject({ text: "a", binary: false });
});

test("body kinds and header lookup", () => {
  expect(bodyKind("application/json; charset=utf-8")).toBe("json");
  expect(bodyKind("application/problem+json")).toBe("json");
  expect(bodyKind("text/html")).toBe("markup");
  expect(bodyKind("application/atom+xml")).toBe("markup");
  expect(bodyKind("application/x-www-form-urlencoded")).toBe("form");
  expect(bodyKind(undefined)).toBe("text");
  expect(headerValue({ "Content-Type": ["text/plain"] }, "content-type")).toBe("text/plain");
});
