import { expect, test } from "vitest";
import { fixtures } from "@/test/fixtures";

// fixtures.ts parses every captured response with its schema, so importing
// it is the test; these assertions pin what later tasks rely on.
test("the captured API responses parse", () => {
  expect(fixtures.me.capName).toBe("mazetti.me/cap/relay");
  expect(fixtures.me.policy.defaults.holdTimeout).toBe("30s");
  expect(fixtures.hooks.find((h) => h.path === "payments/done")?.lastCall).toBeDefined();
  expect(fixtures.callJson.replay).toEqual({ allowed: true, reason: "" });
  expect(fixtures.callPending.replay?.allowed).toBe(false);
  expect(fixtures.callAdmin.replay).toBeUndefined();
  expect(fixtures.callAdmin.requestBody.body).toBeUndefined();
  expect(fixtures.callJson.requestHeaders?.["Content-Type"]).toEqual(["application/json"]);
});
