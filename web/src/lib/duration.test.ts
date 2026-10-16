import { expect, test } from "vitest";
import { formatDuration, humanDuration, humanSpan, parseDuration } from "./duration";

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

test("reads setting durations and lifetimes in words", () => {
  expect(humanDuration(0)).toBe("0 s");
  expect(humanDuration(60_000)).toBe("60 s");
  expect(humanDuration(90_000)).toBe("90 s");
  expect(humanDuration(120_000)).toBe("2 min");
  expect(humanDuration(3_600_000)).toBe("1 h");
  expect(humanDuration(1.5)).toBe("1.5 ms");
  expect(humanSpan(76 * 86_400_000)).toBe("76 d");
  expect(humanSpan(86_340_000)).toBe("1 d");
  expect(humanSpan(5 * 3_600_000)).toBe("5 h");
  expect(humanSpan(30 * 60_000)).toBe("30 min");
  expect(humanSpan(1000)).toBe("1 min");
});
