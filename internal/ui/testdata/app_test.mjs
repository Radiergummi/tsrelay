import { test } from "node:test";
import assert from "node:assert/strict";
import { eventHook, prettyJSON, problemText, wantsEvent, parseHeaders, fieldValue, formRequest } from "../static/app.js";

test("wantsEvent matches listed hooks and *", () => {
  assert.equal(wantsEvent("h_1 h_2", "h_2"), true);
  assert.equal(wantsEvent("h_1", "h_2"), false);
  assert.equal(wantsEvent("*", "h_9"), true);
  assert.equal(wantsEvent(undefined, "h_1"), false);
});

test("eventHook reads call and hook events", () => {
  assert.equal(eventHook('{"id":"c_1","hookId":"h_1"}'), "h_1");
  assert.equal(eventHook('{"id":"h_2"}'), "h_2");
  assert.equal(eventHook("not json"), "");
});

test("problemText prefers the problem's detail", () => {
  assert.equal(problemText({ status: 422, data: { title: "Unprocessable Entity", detail: "path: taken" } }), "path: taken");
  assert.equal(problemText({ status: 502, data: null }), "The relay answered 502.");
});

test("prettyJSON indents JSON and rejects the rest", () => {
  assert.equal(prettyJSON('{"a":[1,2]}'), '{\n  "a": [\n    1,\n    2\n  ]\n}');
  assert.equal(prettyJSON('{"a":'), null);
});

test("parseHeaders reads Name: value lines", () => {
  assert.deepEqual(parseHeaders("Content-Type: application/json\n\nX-A: 1\nX-A: 2:3\nX-Empty"), {
    "Content-Type": ["application/json"],
    "X-A": ["1", "2:3"],
    "X-Empty": [""],
  });
});

test("fieldValue converts and checks each field type", () => {
  assert.deepEqual(fieldValue(" ", "duration"), { value: undefined });
  assert.deepEqual(fieldValue("1m30s", "duration"), { value: "1m30s" });
  assert.ok(fieldValue("30", "duration").error);
  assert.deepEqual(fieldValue("1024", "int"), { value: 1024 });
  assert.ok(fieldValue("1e3", "int").error);
  assert.deepEqual(fieldValue("false", "bool"), { value: false });
  assert.deepEqual(fieldValue("2.5 / 10", "ratelimit"), { value: { perSecond: 2.5, burst: 10 } });
  assert.ok(fieldValue("fast", "ratelimit").error);
  assert.deepEqual(fieldValue("2026-12-31T00:00:00Z", "time"), { value: "2026-12-31T00:00:00Z" });
});

test("formRequest nests fields, and empties mean the default", () => {
  const fields = [
    ["path", "cb", "text"],
    ["channel", "", "text"],
    ["ephemeral", "true", "bool"],
    ["settings.holdTimeout", "45s", "duration"],
    ["settings.idleTimeout", "", "duration"],
  ];
  assert.deepEqual(formRequest(fields, false), {
    body: { path: "cb", ephemeral: true, settings: { holdTimeout: "45s" } },
    errors: [],
  });
  assert.deepEqual(formRequest(fields, true).body, {
    path: "cb", channel: null, ephemeral: true, settings: { holdTimeout: "45s", idleTimeout: null },
  });
  assert.deepEqual(formRequest([["settings.maxBodyBytes", "lots", "int"]], false).errors, [
    { field: "settings.maxBodyBytes", message: "must be a whole number of bytes" },
  ]);
});
