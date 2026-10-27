import { expect, test } from "vitest";
import { fixtures } from "@/test/fixtures";
import { countByStatus, matches, methodsOf, readFilters, sendersOf, writeFilters } from "./filters";

const list = fixtures.busyCalls;

test("filters round-trip through the URL query, leaving other parameters alone", () => {
  const filters = readFilters(
    new URLSearchParams("status=5xx&method=PUT&path=item%2F1&sender=tag%3Abilling&x=1"),
  );
  expect(filters).toEqual({ status: "5xx", method: "PUT", path: "item/1", sender: "tag:billing" });
  expect(
    writeFilters({ ...filters, method: "" }, new URLSearchParams("x=1&method=PUT")).toString(),
  ).toBe("x=1&status=5xx&path=item%2F1&sender=tag%3Abilling");
  expect(readFilters(new URLSearchParams("status=bogus")).status).toBe("all");
});

test("matching, counts and options", () => {
  const counts = countByStatus(list);
  expect(counts.all).toBe(100);
  expect(counts["2xx"] + counts["3xx"] + counts["4xx"] + counts["5xx"]).toBe(100);
  expect(counts.failed).toBeGreaterThan(0);
  expect(
    list
      .filter((call) => matches(call, { status: "failed", method: "", path: "", sender: "" }))
      .every(({ outcome }) => outcome !== "ok"),
  ).toBe(true);
  expect(
    list
      .filter((call) => matches(call, { status: "all", method: "", path: "/item/4", sender: "" }))
      .map(({ path }) => path)
      .sort(),
  ).toEqual(["/busy/item/4", ...Array.from({ length: 10 }, (_, i) => `/busy/item/4${i}`)].sort());
  expect(methodsOf(list)).toEqual(["GET", "POST", "PUT"]);
  expect(sendersOf(list)).toEqual(["tag:billing", "tag:prod-worker"]);
});
