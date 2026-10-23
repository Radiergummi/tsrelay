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

/** humanDuration reads a setting's duration in words: "10 s", "60 s", "90 s", "2 min", "1 h". */
export function humanDuration(ms: number): string {
  if (ms >= 3_600_000 && ms % 3_600_000 === 0) return `${ms / 3_600_000} h`;
  if (ms >= 120_000 && ms % 60_000 === 0) return `${ms / 60_000} min`;
  if (ms >= 1000 || ms === 0) return `${trim(ms / 1000)} s`;
  return `${trim(ms)} ms`;
}

/** humanSpan reads a lifetime coarsely, in whole days, hours or minutes: "76 d", "5 h", "30 min". */
export function humanSpan(ms: number): string {
  const hours = Math.round(ms / 3_600_000);
  const minutes = Math.round(ms / 60_000);
  if (hours >= 24) return `${Math.round(ms / 86_400_000)} d`;
  if (minutes >= 60) return `${hours} h`;
  return `${Math.max(1, minutes)} min`;
}

const trim = (n: number) => String(Number(n.toFixed(3)));
