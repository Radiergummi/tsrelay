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
export function parseDuration(goDuration: string): number | null {
  if (goDuration === "0") return 0;
  if (!whole.test(goDuration)) return null;
  let milliseconds = 0;
  for (const [, amount, unit] of goDuration.matchAll(part))
    milliseconds += Number(amount) * units[unit!]!;
  return milliseconds;
}

/** formatDuration shows a call's duration to the millisecond, or to the microsecond below that. */
export function formatDuration(milliseconds: number): string {
  if (milliseconds < 1) return `${Math.round(milliseconds * 1000)}µs`;
  if (milliseconds < 1000) return `${Math.round(milliseconds)}ms`;
  if (milliseconds < 60_000) return `${trim(milliseconds / 1000)}s`;
  const minutes = Math.floor(milliseconds / 60_000);
  return `${minutes}m${trim((milliseconds - minutes * 60_000) / 1000)}s`;
}

/** humanDuration reads a setting's duration in words: "10 s", "60 s", "90 s", "2 min", "1 h". */
export function humanDuration(milliseconds: number): string {
  if (milliseconds >= 3_600_000 && milliseconds % 3_600_000 === 0)
    return `${milliseconds / 3_600_000} h`;
  if (milliseconds >= 120_000 && milliseconds % 60_000 === 0) return `${milliseconds / 60_000} min`;
  if (milliseconds >= 1000 || milliseconds === 0) return `${trim(milliseconds / 1000)} s`;
  return `${trim(milliseconds)} ms`;
}

/** humanSpan reads a lifetime coarsely, in whole days, hours or minutes: "76 d", "5 h", "30 min". */
export function humanSpan(milliseconds: number): string {
  const hours = Math.round(milliseconds / 3_600_000);
  const minutes = Math.round(milliseconds / 60_000);
  if (hours >= 24) return `${Math.round(milliseconds / 86_400_000)} d`;
  if (minutes >= 60) return `${hours} h`;
  return `${Math.max(1, minutes)} min`;
}

const trim = (value: number) => String(Number(value.toFixed(3)));
