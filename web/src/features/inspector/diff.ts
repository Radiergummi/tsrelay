/** Larger inputs aren't diffed: the table is lines × lines. */
const maxCells = 4_000_000;

export type DiffLine = { op: "same" | "add" | "del"; text: string };

/** diffLines is a line diff by longest common subsequence: before's lines removed, after's added. */
export function diffLines(before: string[], after: string[]): DiffLine[] | null {
  if (before.length * after.length > maxCells) return null;
  const beforeCount = before.length;
  const afterCount = after.length;
  const lcs = Array.from({ length: beforeCount + 1 }, () => new Uint32Array(afterCount + 1));
  for (let i = beforeCount - 1; i >= 0; i--) {
    for (let j = afterCount - 1; j >= 0; j--) {
      lcs[i]![j] =
        before[i] === after[j]
          ? lcs[i + 1]![j + 1]! + 1
          : Math.max(lcs[i + 1]![j]!, lcs[i]![j + 1]!);
    }
  }
  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < beforeCount && j < afterCount) {
    if (before[i] === after[j]) {
      out.push({ op: "same", text: before[i]! });
      i++;
      j++;
    } else if (lcs[i + 1]![j]! >= lcs[i]![j + 1]!) out.push({ op: "del", text: before[i++]! });
    else out.push({ op: "add", text: after[j++]! });
  }
  while (i < beforeCount) out.push({ op: "del", text: before[i++]! });
  while (j < afterCount) out.push({ op: "add", text: after[j++]! });
  return out;
}
