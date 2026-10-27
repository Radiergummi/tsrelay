/** Bodies above this show their first part only; captures are 64 KiB by default. */
const hexLimit = 64 * 1024;

const hex = (byte: number) => byte.toString(16).padStart(2, "0");

/** HexView shows bytes as offset, hex and ASCII columns, 16 bytes a line. */
export function HexView({ bytes }: { bytes: Uint8Array }) {
  const shown = bytes.subarray(0, hexLimit);
  const lines: string[] = [];
  for (let offset = 0; offset < shown.length; offset += 16) {
    const row = shown.subarray(offset, offset + 16);
    const hexColumn = [...row].map(hex).join(" ").padEnd(47, " ");
    const asciiColumn = [...row]
      .map((byte) => (byte >= 0x20 && byte < 0x7f ? String.fromCharCode(byte) : "."))
      .join("");
    lines.push(`${offset.toString(16).padStart(8, "0")}  ${hexColumn}  ${asciiColumn}`);
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
