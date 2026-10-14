const units: Record<string, number> = {
  ns: 1e-6,
  us: 1e-3,
  µs: 1e-3,
  μs: 1e-3,
  ms: 1,
  s: 1000,
  m: 60_000,
  h: 3_600_000,
};
const whole = /^(\d+(\.\d+)?(ns|us|µs|μs|ms|s|m|h))+$/;
const part = /(\d+(?:\.\d+)?)(ns|us|µs|μs|ms|s|m|h)/g;

/** parseDuration reads a Go duration string ("1m30s", "1.5ms") as milliseconds. */
export function parseDuration(go: string): number | null {
  if (go === "0") return 0;
  if (!whole.test(go)) return null;
  let ms = 0;
  for (const [, n, unit] of go.matchAll(part)) ms += Number(n) * units[unit!]!;
  return ms;
}

/** formatDuration shows a call's duration to the millisecond, or to the microsecond below that. */
export function formatDuration(ms: number): string {
  if (ms < 1) return `${Math.round(ms * 1000)}µs`;
  if (ms < 1000) return `${Math.round(ms)}ms`;
  if (ms < 60_000) return `${trim(ms / 1000)}s`;
  const m = Math.floor(ms / 60_000);
  return `${m}m${trim((ms - m * 60_000) / 1000)}s`;
}

/** formatGoDuration formats a Go duration string, showing an unreadable one as 0. */
export const formatGoDuration = (go: string) => formatDuration(parseDuration(go) ?? 0);

const trim = (n: number) => String(Number(n.toFixed(3)));
