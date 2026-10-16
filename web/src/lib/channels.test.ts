import { expect, test } from "vitest";
import { allowingPattern, channelChoices, matchChannel, validChannel } from "./channels";

test("channel patterns match like authz.MatchChannel", () => {
  expect(matchChannel("*", "default")).toBe(true);
  expect(matchChannel("prod", "prod")).toBe(true);
  expect(matchChannel("staging.*", "staging.eu")).toBe(true);
  expect(matchChannel("staging.*", "staging")).toBe(false);
  expect(matchChannel("a*c", "abbbc")).toBe(true);
  expect(matchChannel("a*c", "abcd")).toBe(false);
});

test("choices are default when a pattern allows it, then every literal pattern", () => {
  expect(channelChoices(["default", "prod", "staging.*"])).toEqual(["default", "prod"]);
  expect(channelChoices(["*"])).toEqual(["default"]);
  expect(channelChoices(["prod.*"])).toEqual([]);
});

test("channel names are validated like authz.ValidChannel, and the allowing pattern found", () => {
  expect(validChannel("team-a.eu")).toBe(true);
  expect(validChannel("Team")).toBe(false);
  expect(validChannel("a..b")).toBe(false);
  expect(validChannel("a".repeat(65))).toBe(false);
  expect(allowingPattern(["*", "prod"], "prod")).toBe("prod");
  expect(allowingPattern(["staging.*"], "staging.eu")).toBe("staging.*");
  expect(allowingPattern(["staging.*"], "prod")).toBeUndefined();
});
