/** How far a delay may fall either side of its nominal value, so clients that failed together don't retry together. */
const jitter = 0.25;

/**
 * backoffDelay is the delay before retry number attempt (1-based): base,
 * doubling up to max, then ±25% of jitter (upper bound exclusive).
 */
export function backoffDelay(
  attempt: number,
  { base, max }: { base: number; max: number },
  random = Math.random,
): number {
  const nominal = Math.min(base * 2 ** (Math.max(1, attempt) - 1), max);
  return Math.floor(nominal * (1 - jitter + random() * jitter * 2));
}
