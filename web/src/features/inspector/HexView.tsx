/** Bodies above this show their first part only; captures are 64 KiB by default. */
const hexLimit = 64 * 1024;

const hex = (b: number) => b.toString(16).padStart(2, "0");

/** HexView shows bytes as offset, hex and ASCII columns, 16 bytes a line. */
export function HexView({ bytes }: { bytes: Uint8Array }) {
  const shown = bytes.subarray(0, hexLimit);
  const lines: string[] = [];
  for (let off = 0; off < shown.length; off += 16) {
    const row = shown.subarray(off, off + 16);
    const h = [...row].map(hex).join(" ").padEnd(47, " ");
    const a = [...row].map((b) => (b >= 0x20 && b < 0x7f ? String.fromCharCode(b) : ".")).join("");
    lines.push(`${off.toString(16).padStart(8, "0")}  ${h}  ${a}`);
  }
  return (
    <div>
      <pre className="max-h-[60vh] overflow-auto rounded-md border bg-muted/40 p-3 text-xs leading-relaxed">
        {lines.join("\n")}
      </pre>
      {bytes.length > hexLimit && (
        <p className="mt-1 text-xs text-muted-foreground">Showing the first 64 KiB.</p>
      )}
    </div>
  );
}
