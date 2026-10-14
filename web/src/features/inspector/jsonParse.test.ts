import { expect, test } from "vitest";
import { parseJSON } from "./jsonParse";

test("keeps number literals and duplicate keys", () => {
  expect(parseJSON(`{"amount": 12345678901234567890, "a": 1, "a": 2.50}`)).toEqual({
    type: "object",
    entries: [
      ["amount", { type: "number", raw: "12345678901234567890" }],
      ["a", { type: "number", raw: "1" }],
      ["a", { type: "number", raw: "2.50" }],
    ],
  });
});

test("strings are unescaped, and invalid or truncated JSON is null", () => {
  expect(parseJSON(`["a\\u0041\\n", true, null]`)).toEqual({
    type: "array",
    items: [
      { type: "string", value: "aA\n" },
      { type: "literal", raw: "true" },
      { type: "literal", raw: "null" },
    ],
  });
  expect(parseJSON(`{"a": 1`)).toBeNull();
  expect(parseJSON(`{"a": 1} trailing`)).toBeNull();
  expect(parseJSON(`01`)).toBeNull();
});

test("deep nesting is refused, not a stack overflow", () => {
  expect(parseJSON("[".repeat(100_000) + "]".repeat(100_000))).toBeNull();
});
