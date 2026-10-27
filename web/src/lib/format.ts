import type { Sender } from "@/api/schemas";
import { reasonPhrase, relayPhrase } from "./httpStatus";

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1 << 20) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / (1 << 20)).toFixed(1)} MiB`;
}

/** rfc3339 is a time in UTC to the second, as the API takes it: "2026-12-31T17:00:00Z". */
export const rfc3339 = (date: Date) => date.toISOString().replace(/\.\d{3}Z$/, "Z");

/** localInputValue is an RFC 3339 time as an <input type="datetime-local"> shows it: in the browser's time zone, to the minute. */
export function localInputValue(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

/** fromLocalInput reads an <input type="datetime-local"> value, which is local time, as RFC 3339 in UTC; "" if it isn't a time. */
export function fromLocalInput(local: string): string {
  const date = new Date(local); // a date and time without an offset is local time
  return local === "" || Number.isNaN(date.getTime()) ? "" : rfc3339(date);
}

/** absoluteTime is an RFC 3339 time in UTC to the second: "2026-10-14 11:59:58Z". */
export function absoluteTime(iso: string): string {
  return new Date(iso).toISOString().slice(0, 19).replace("T", " ") + "Z";
}

/** relativeTime counts seconds up to 30 s, then "less than a minute", then whole minutes, hours, days. */
export function relativeTime(iso: string, now = Date.now()): string {
  const seconds = Math.max(0, Math.floor((now - Date.parse(iso)) / 1000));
  if (seconds <= 30) return `${seconds} s ago`;
  if (seconds < 60) return "less than a minute ago";
  if (seconds < 3600) return `${Math.floor(seconds / 60)} min ago`;
  if (seconds < 86_400) return `${Math.floor(seconds / 3600)} h ago`;
  return `${Math.floor(seconds / 86_400)} d ago`;
}

/** senderLabel names a call's sender: its tags, else its user, else its node or IP. */
export function senderLabel(sender: Sender): string {
  const { ip, tags, login, node } = sender;
  if (sender.public) return `Internet · ${ip || "unknown"}`;
  if (tags?.length) return tags.join(", ");
  return login || node || ip || "unknown";
}

type Outcome = { outcome: string; status?: number | undefined };

/** failed: the relay could not deliver the call (anything but "ok" and "pending"). */
export function failed({ outcome }: Outcome): boolean {
  return outcome !== "ok" && outcome !== "pending";
}

export function statusClass(
  call: Outcome,
): "2xx" | "3xx" | "4xx" | "5xx" | "pending" | "failed" | "other" {
  if (call.outcome === "pending") return "pending";
  const status = call.status ?? 0;
  if (status >= 200 && status < 300) return "2xx";
  if (status >= 300 && status < 400) return "3xx";
  if (status >= 400 && status < 500) return "4xx";
  if (status >= 500 && status < 600) return "5xx";
  return failed(call) ? "failed" : "other";
}

/**
 * outcomeLabel: "200 OK" for a delivered call, "563 Tunnel lost" for a status
 * of the relay's 56x range, "404 hook_not_found" for another relay error, "pending".
 */
export function outcomeLabel({ outcome, status = 0 }: Outcome): string {
  if (outcome === "pending") return "pending";
  const code = status ? String(status) : "";
  const rest = relayPhrase(status) || (outcome === "ok" ? reasonPhrase(status) : outcome);
  return [code, rest].filter(Boolean).join(" ");
}
