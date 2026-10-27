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

export function readFilters(params: URLSearchParams): Filters {
  const status = params.get("status") as StatusFilter | null;
  return {
    status: status && statusFilters.includes(status) ? status : "all",
    method: params.get("method") ?? "",
    path: params.get("path") ?? "",
    sender: params.get("sender") ?? "",
  };
}

export function writeFilters(
  { status, method, path, sender }: Filters,
  params: URLSearchParams,
): URLSearchParams {
  const out = new URLSearchParams(params);
  const set = (key: string, value: string, empty: string) =>
    value === empty ? out.delete(key) : out.set(key, value);
  set("status", status, "all");
  set("method", method, "");
  set("path", path, "");
  set("sender", sender, "");
  return out;
}

function inStatus(call: Summary, filter: StatusFilter): boolean {
  if (filter === "all") return true;
  if (filter === "failed") return failed(call);
  return statusClass(call) === filter;
}

/** matches filters in the browser, over the calls the relay keeps (a substring match on the path). */
export function matches(call: Summary, { status, method, path, sender }: Filters): boolean {
  return (
    inStatus(call, status) &&
    (!method || call.method === method) &&
    (!path || call.path.includes(path)) &&
    (!sender || senderLabel(call.sender) === sender)
  );
}

export function countByStatus(list: Summary[]): Record<StatusFilter, number> {
  return Object.fromEntries(
    statusFilters.map((status) => [status, list.filter((call) => inStatus(call, status)).length]),
  ) as Record<StatusFilter, number>;
}

export const methodsOf = (list: Summary[]) => [...new Set(list.map(({ method }) => method))].sort();
export const sendersOf = (list: Summary[]) =>
  [...new Set(list.map(({ sender }) => senderLabel(sender)))].sort();
