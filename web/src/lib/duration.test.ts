import { expect, test } from "vitest";
import { formatDuration, parseDuration } from "./duration";

test("parses Go durations", () => {
  expect(parseDuration("0s")).toBe(0);
  expect(parseDuration("30s")).toBe(30_000);
  expect(parseDuration("2m0s")).toBe(120_000);
  expect(parseDuration("1h30m")).toBe(5_400_000);
  expect(parseDuration("1.5ms")).toBe(1.5);
  expect(parseDuration("250µs")).toBe(0.25);
  expect(parseDuration("250us")).toBe(0.25);
  expect(parseDuration("800ns")).toBeCloseTo(0.0008);
  expect(parseDuration("")).toBeNull();
  expect(parseDuration("5 minutes")).toBeNull();
  expect(parseDuration("-1s")).toBeNull();
});

test("formats call durations to the millisecond, or the microsecond below that", () => {
  expect(formatDuration(0.25)).toBe("250µs");
  expect(formatDuration(42.4)).toBe("42ms");
  expect(formatDuration(1234)).toBe("1.234s");
  expect(formatDuration(61_500)).toBe("1m1.5s");
});
