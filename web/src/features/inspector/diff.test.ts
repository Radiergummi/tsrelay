import { expect, test } from "vitest";
import { diffLines } from "./diff";

test("marks lines added and removed, keeping the common ones", () => {
  expect(diffLines(["a", "b", "c"], ["a", "x", "c", "d"])).toEqual([
    { op: "same", text: "a" },
    { op: "del", text: "b" },
    { op: "add", text: "x" },
    { op: "same", text: "c" },
    { op: "add", text: "d" },
  ]);
  expect(diffLines([], [])).toEqual([]);
});

test("refuses inputs too large to diff", () => {
  const big = Array.from({ length: 5000 }, (_, i) => String(i));
  expect(
    diffLines(
      big,
      big.map((x) => x + "!"),
    ),
  ).toBeNull();
});
