import { CopyButton } from "@/components/ui/copy-button";
import { headerLines } from "./curl";
import { Reveal } from "./Reveal";

/** HeaderTable lists headers one value per row, sorted, with the redacted ones marked. */
export function HeaderTable({
  headers,
  redacted,
  metadataOnly,
}: {
  headers: Record<string, string[]> | undefined;
  redacted: string[] | undefined;
  metadataOnly: boolean;
}) {
  if (metadataOnly)
    return (
      <p className="text-muted-foreground">
        Headers are not shown: only the hook's owner sees them.
      </p>
    );
  const lines = headerLines(headers);
  if (lines.length === 0 && !redacted?.length)
    return <p className="text-muted-foreground">No headers.</p>;
  return (
    <table className="w-full text-xs">
      <tbody>
        {lines.map(([name, value], i) => (
          <tr key={i} className="border-b align-top last:border-0">
            <th className="w-1/3 py-1 pr-3 text-left font-mono font-medium break-all">{name}</th>
            <td className="py-1 font-mono break-all">
              <Reveal text={value} />
            </td>
            <td className="w-6 py-0.5">
              <CopyButton text={value} label={`${name} value`} />
            </td>
          </tr>
        ))}
        {redacted?.map((name) => (
          <tr key={`r-${name}`} className="border-b align-top last:border-0">
            <th className="py-1 pr-3 text-left font-mono font-medium">{name}</th>
            <td className="py-1 text-muted-foreground" colSpan={2}>
              left out (redacted: never captured)
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
