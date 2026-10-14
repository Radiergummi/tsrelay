import type { Summary } from "@/api/schemas";
import { failed, senderLabel, statusClass } from "@/lib/format";

export type StatusFilter = "all" | "2xx" | "3xx" | "4xx" | "5xx" | "failed" | "pending";
export const statusFilters: StatusFilter[] = [
  "all",
  "2xx",
  "3xx",
  "4xx",
  "5xx",
  "failed",
  "pending",
];

export interface Filters {
  status: StatusFilter;
  method: string;
  path: string;
  sender: string;
}

export function readFilters(p: URLSearchParams): Filters {
  const status = p.get("status") as StatusFilter | null;
  return {
    status: status && statusFilters.includes(status) ? status : "all",
    method: p.get("method") ?? "",
    path: p.get("path") ?? "",
    sender: p.get("sender") ?? "",
  };
}

export function writeFilters(f: Filters, p: URLSearchParams): URLSearchParams {
  const out = new URLSearchParams(p);
  const set = (k: string, v: string, empty: string) =>
    v === empty ? out.delete(k) : out.set(k, v);
  set("status", f.status, "all");
  set("method", f.method, "");
  set("path", f.path, "");
  set("sender", f.sender, "");
  return out;
}

function inStatus(s: Summary, f: StatusFilter): boolean {
  if (f === "all") return true;
  if (f === "failed") return failed(s);
  return statusClass(s) === f;
}

/** matches filters in the browser, over the calls the relay keeps (a substring match on the path). */
export function matches(s: Summary, f: Filters): boolean {
  return (
    inStatus(s, f.status) &&
    (!f.method || s.method === f.method) &&
    (!f.path || s.path.includes(f.path)) &&
    (!f.sender || senderLabel(s.sender) === f.sender)
  );
}

export function countByStatus(list: Summary[]): Record<StatusFilter, number> {
  return Object.fromEntries(
    statusFilters.map((f) => [f, list.filter((s) => inStatus(s, f)).length]),
  ) as Record<StatusFilter, number>;
}

export const methodsOf = (list: Summary[]) => [...new Set(list.map((s) => s.method))].sort();
export const sendersOf = (list: Summary[]) =>
  [...new Set(list.map((s) => senderLabel(s.sender)))].sort();
