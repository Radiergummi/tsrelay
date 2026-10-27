import { expect, test } from "vitest";
import { fixtures } from "@/test/fixtures";
import {
  effectiveSettings,
  fieldValue,
  formRequest,
  initialValues,
  parseHeaders,
} from "./formRequest";

test("field values by type", () => {
  expect(fieldValue("  ", "duration")).toEqual({ value: undefined });
  expect(fieldValue("1h30m", "duration")).toEqual({ value: "1h30m" });
  // Go accepts both the micro sign and the Greek mu, as parseDuration does.
  expect(fieldValue("1µs", "duration")).toEqual({ value: "1µs" });
  expect(fieldValue("1μs", "duration")).toEqual({ value: "1μs" });
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

test("creating leaves empty fields out", () => {
  const values = {
    path: "cb",
    description: "",
    "settings.holdTimeout": "45s",
    "settings.idleTimeout": "",
  };
  expect(formRequest(values)).toEqual({
    body: { path: "cb", settings: { holdTimeout: "45s" } },
    errors: [],
  });
  expect(formRequest({ "settings.holdTimeout": "soon" }).errors).toEqual([
    { field: "settings.holdTimeout", message: "must be a duration like 30s, 2m or 1h30m" },
  ]);
});

test("editing sends only the changed fields, and null for a cleared one", () => {
  const initial = {
    ...initialValues(undefined),
    path: "cb",
    description: "old",
    "settings.holdTimeout": "10m",
    "settings.idleTimeout": "",
  };
  const values = {
    path: "cb",
    description: "",
    "settings.holdTimeout": "10m",
    "settings.idleTimeout": "45s",
  };
  expect(formRequest(values, initial)).toEqual({
    body: { description: null, settings: { idleTimeout: "45s" } },
    errors: [],
  });
  expect(formRequest({ path: " cb " }, initial).body).toEqual({});
});

test("initial values come from the hook, with relay defaults left empty", () => {
  const values = initialValues(fixtures.hook);
  expect(values["path"]).toBe("payments/done");
  expect(values["settings.holdTimeout"]).toBe("");
  expect(initialValues(undefined)["ephemeral"]).toBe("false");
});

test("effective settings are the valid overrides over the relay defaults", () => {
  const values = initialValues(undefined);
  values["settings.holdTimeout"] = "2m";
  values["settings.idleTimeout"] = "soon";
  values["settings.capture"] = "false";
  expect(effectiveSettings(values, fixtures.me.policy.defaults)).toEqual({
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
  const { body } = formRequest({ public: "true" });
  expect(body).toEqual({ public: true });
});

test("headers are Name: value lines", () => {
  expect(parseHeaders("X-A: 1\n\nX-A: 2\nX-Empty\n  X-B :  v : w ")).toEqual({
    "X-A": ["1", "2"],
    "X-Empty": [""],
    "X-B": ["v : w"],
  });
});
