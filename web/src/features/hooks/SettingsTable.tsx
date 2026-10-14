import type { Hook } from "@/api/schemas";
import { formatSize } from "@/lib/format";

/** SettingsTable lists a hook's effective settings (DESIGN.md §8), marking those left at the relay default. */
export function SettingsTable({ hook }: { hook: Hook }) {
  const s = hook.settings;
  const e = hook.effective;
  const rows: [string, string, boolean][] = [
    ["Attach policy", e.attachPolicy, s.attachPolicy === undefined],
    ["Host header", e.hostHeader, s.hostHeader === undefined],
    ["Hold timeout", e.holdTimeout === "0s" ? "off" : e.holdTimeout, s.holdTimeout === undefined],
    ["Response timeout", e.responseTimeout, s.responseTimeout === undefined],
    ["Idle timeout", e.idleTimeout, s.idleTimeout === undefined],
    [
      "Max body size",
      e.maxBodyBytes > 0 ? formatSize(e.maxBodyBytes) : "unlimited",
      s.maxBodyBytes === undefined,
    ],
    [
      "Rate limit",
      `${e.rateLimit.perSecond}/s, burst ${e.rateLimit.burst}`,
      s.rateLimit === undefined,
    ],
    ["Capture", e.capture ? "bodies and metadata" : "metadata only", s.capture === undefined],
  ];
  return (
    <section>
      <h2 className="mb-2 font-medium">Settings</h2>
      <p className="mb-3 text-muted-foreground">Select a call to inspect it.</p>
      <table className="w-full max-w-lg">
        <tbody>
          {rows.map(([name, value, dflt]) => (
            <tr key={name} className="border-b last:border-0">
              <th className="py-1.5 pr-4 text-left font-normal text-muted-foreground">{name}</th>
              <td className="py-1.5">
                {value}
                {dflt && <span className="ml-2 text-xs text-muted-foreground">relay default</span>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
