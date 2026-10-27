import { expect, test } from "vitest";
import { backoffDelay } from "./backoff";

test("doubles up to the ceiling, with a quarter of jitter either side", () => {
  const mid = () => 0.5; // no jitter
  expect(
    [1, 2, 3, 4, 10].map((attempt) => backoffDelay(attempt, { base: 1000, max: 30_000 }, mid)),
  ).toEqual([1000, 2000, 4000, 8000, 30_000]);
  expect(backoffDelay(1, { base: 1000, max: 30_000 }, () => 0)).toBe(750);
  expect(backoffDelay(1, { base: 1000, max: 30_000 }, () => 0.999999)).toBeLessThan(1250);
});
