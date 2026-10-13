import { test } from "node:test";
import assert from "node:assert/strict";
import { eventHook, prettyJSON, problemText, wantsEvent, parseHeaders } from "../static/app.js";

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
