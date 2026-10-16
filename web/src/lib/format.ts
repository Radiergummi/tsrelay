import type { Sender } from "@/api/schemas";
import { reasonPhrase } from "./httpStatus";

export function formatSize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1 << 20) return `${(n / 1024).toFixed(1)} KiB`;
  return `${(n / (1 << 20)).toFixed(1)} MiB`;
}

/** rfc3339 is a time in UTC to the second, as the API takes it: "2026-12-31T17:00:00Z". */
export const rfc3339 = (d: Date) => d.toISOString().replace(/\.\d{3}Z$/, "Z");

/** localInputValue is an RFC 3339 time as an <input type="datetime-local"> shows it: in the browser's time zone, to the minute. */
export function localInputValue(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

/** fromLocalInput reads an <input type="datetime-local"> value, which is local time, as RFC 3339 in UTC; "" if it isn't a time. */
export function fromLocalInput(local: string): string {
  const d = new Date(local); // a date and time without an offset is local time
  return local === "" || Number.isNaN(d.getTime()) ? "" : rfc3339(d);
}

/** absoluteTime is an RFC 3339 time in UTC to the second: "2026-10-14 11:59:58Z". */
export function absoluteTime(iso: string): string {
  return new Date(iso).toISOString().slice(0, 19).replace("T", " ") + "Z";
}

export function relativeTime(iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - Date.parse(iso)) / 1000));
  if (s < 10) return "just now";
  if (s < 60) return `${s} s ago`;
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86_400) return `${Math.floor(s / 3600)} h ago`;
  return `${Math.floor(s / 86_400)} d ago`;
}

/** senderLabel names a call's sender: its tags, else its user, else its node or IP. */
export function senderLabel(s: Sender): string {
  if (s.tags?.length) return s.tags.join(", ");
  return s.login || s.node || s.ip || "unknown";
}

type Outcome = { outcome: string; status?: number | undefined };

/** failed: the relay could not deliver the call (anything but "ok" and "pending"). */
export function failed(c: Outcome): boolean {
  return c.outcome !== "ok" && c.outcome !== "pending";
}

export function statusClass(
  c: Outcome,
): "2xx" | "3xx" | "4xx" | "5xx" | "pending" | "failed" | "other" {
  if (c.outcome === "pending") return "pending";
  const st = c.status ?? 0;
  if (st >= 200 && st < 300) return "2xx";
  if (st >= 300 && st < 400) return "3xx";
  if (st >= 400 && st < 500) return "4xx";
  if (st >= 500 && st < 600) return "5xx";
  return failed(c) ? "failed" : "other";
}

/** outcomeLabel: "200 OK" for a delivered call, "404 hook_not_found" for a relay error, "pending". */
export function outcomeLabel(c: Outcome): string {
  if (c.outcome === "pending") return "pending";
  const st = c.status ? String(c.status) : "";
  const rest = c.outcome === "ok" ? reasonPhrase(c.status ?? 0) : c.outcome;
  return [st, rest].filter(Boolean).join(" ");
}
