import { expect, test } from "vitest";
import {
  absoluteTime,
  formatSize,
  outcomeLabel,
  relativeTime,
  senderLabel,
  statusClass,
} from "./format";
import { reasonPhrase } from "./httpStatus";

test("sizes", () => {
  expect(formatSize(0)).toBe("0 B");
  expect(formatSize(1023)).toBe("1023 B");
  expect(formatSize(4096)).toBe("4.0 KiB");
  expect(formatSize(1 << 20)).toBe("1.0 MiB");
});

test("times", () => {
  const now = Date.parse("2026-10-14T12:00:00Z");
  expect(absoluteTime("2026-10-14T11:59:58.123456789Z")).toBe("2026-10-14 11:59:58Z");
  expect(relativeTime("2026-10-14T11:59:58Z", now)).toBe("just now");
  expect(relativeTime("2026-10-14T11:58:00Z", now)).toBe("2 min ago");
  expect(relativeTime("2026-10-14T09:00:00Z", now)).toBe("3 h ago");
  expect(relativeTime("2026-10-11T12:00:00Z", now)).toBe("3 d ago");
});

test("status classes and labels", () => {
  expect(statusClass({ outcome: "ok", status: 204 })).toBe("2xx");
  expect(statusClass({ outcome: "ok", status: 302 })).toBe("3xx");
  expect(statusClass({ outcome: "pending" })).toBe("pending");
  expect(statusClass({ outcome: "upstream_unreachable", status: 502 })).toBe("5xx");
  expect(statusClass({ outcome: "tunnel_lost" })).toBe("failed");
  expect(outcomeLabel({ outcome: "ok", status: 200 })).toBe("200 OK");
  expect(outcomeLabel({ outcome: "hook_not_found", status: 404 })).toBe("404 hook_not_found");
  expect(outcomeLabel({ outcome: "pending" })).toBe("pending");
  expect(reasonPhrase(418)).toBe("I'm a teapot");
  expect(reasonPhrase(299)).toBe("");
  expect(senderLabel({ node: "worker", tags: ["tag:prod-worker"] })).toBe("tag:prod-worker");
  expect(senderLabel({ node: "laptop", login: "dev@example.com" })).toBe("dev@example.com");
  expect(senderLabel({ ip: "100.64.0.9" })).toBe("100.64.0.9");
  expect(senderLabel({})).toBe("unknown");
});

test("an internet sender is labelled with its IP", () => {
  expect(senderLabel({ public: true, ip: "203.0.113.7" })).toBe("Internet · 203.0.113.7");
});
