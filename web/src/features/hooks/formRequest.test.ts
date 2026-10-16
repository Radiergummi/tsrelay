import { expect, test } from "vitest";
import { fixtures } from "@/test/fixtures";
import {
  effectiveSettings,
  fieldTypes,
  fieldValue,
  formRequest,
  initialValues,
  parseHeaders,
} from "./formRequest";

test("field values by type", () => {
  expect(fieldValue("  ", "duration")).toEqual({ value: undefined });
  expect(fieldValue("1h30m", "duration")).toEqual({ value: "1h30m" });
  expect(fieldValue("5 minutes", "duration").error).toMatch(/duration like 30s/);
  expect(fieldValue("1024", "int")).toEqual({ value: 1024 });
  expect(fieldValue("1e3", "int").error).toMatch(/whole number/);
  expect(fieldValue("10/20", "ratelimit")).toEqual({ value: { perSecond: 10, burst: 20 } });
  expect(fieldValue("0.5 / 1", "ratelimit")).toEqual({ value: { perSecond: 0.5, burst: 1 } });
  expect(fieldValue("fast", "ratelimit").error).toMatch(
    /calls per second and a whole-number burst/,
  );
  expect(fieldValue("false", "bool")).toEqual({ value: false });
});

test("creating leaves empty fields out; editing resets them with null", () => {
  const fields: [string, string, "text" | "duration"][] = [
    ["path", "cb", "text"],
    ["description", "", "text"],
    ["settings.holdTimeout", "45s", "duration"],
    ["settings.idleTimeout", "", "duration"],
  ];
  expect(formRequest(fields, false)).toEqual({
    body: { path: "cb", settings: { holdTimeout: "45s" } },
    errors: [],
  });
  expect(formRequest(fields, true).body).toEqual({
    path: "cb",
    description: null,
    settings: { holdTimeout: "45s", idleTimeout: null },
  });
  expect(formRequest([["settings.holdTimeout", "soon", "duration"]], false).errors).toEqual([
    { field: "settings.holdTimeout", message: "must be a duration like 30s, 2m or 1h30m" },
  ]);
});

test("initial values come from the hook, with relay defaults left empty", () => {
  const v = initialValues(fixtures.hook);
  expect(v["path"]).toBe("payments/done");
  expect(v["settings.holdTimeout"]).toBe("");
  expect(initialValues(undefined)["ephemeral"]).toBe("false");
});

test("effective settings are the valid overrides over the relay defaults", () => {
  const v = initialValues(undefined);
  v["settings.holdTimeout"] = "2m";
  v["settings.idleTimeout"] = "soon";
  v["settings.capture"] = "false";
  expect(effectiveSettings(v, fixtures.me.policy.defaults)).toEqual({
    ...fixtures.me.policy.defaults,
    holdTimeout: "2m",
    capture: false,
  });
});

test("public is a bool field that starts false and follows the hook", () => {
  expect(initialValues(undefined)["public"]).toBe("false");
  expect(
    initialValues({ ...fixtures.hook, public: true, publicUrl: "https://x/+t" })["public"],
  ).toBe("true");
  const { body } = formRequest([["public", "true", fieldTypes["public"]!]], false);
  expect(body).toEqual({ public: true });
});

test("headers are Name: value lines", () => {
  expect(parseHeaders("X-A: 1\n\nX-A: 2\nX-Empty\n  X-B :  v : w ")).toEqual({
    "X-A": ["1", "2"],
    "X-Empty": [""],
    "X-B": ["v : w"],
  });
});
