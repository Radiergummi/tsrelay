import { expect, test } from "vitest";
import { channelChoices, matchChannel } from "./channels";

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
