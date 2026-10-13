import { test } from "node:test";
import assert from "node:assert/strict";
import { eventHook, problemText, wantsEvent } from "../static/app.js";

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
