import type { Hook } from "@/api/schemas";
import { formatSize } from "@/lib/format";

/** SettingsTable lists a hook's effective settings (DESIGN.md §8), marking those left at the relay default. */
export function SettingsTable({ hook }: { hook: Hook }) {
  const { settings, effective } = hook;
  const rows: [string, string, boolean][] = [
    ["Attach policy", effective.attachPolicy, settings.attachPolicy === undefined],
    ["Host header", effective.hostHeader, settings.hostHeader === undefined],
    [
      "Hold timeout",
      effective.holdTimeout === "0s" ? "off" : effective.holdTimeout,
      settings.holdTimeout === undefined,
    ],
    ["Response timeout", effective.responseTimeout, settings.responseTimeout === undefined],
    ["Idle timeout", effective.idleTimeout, settings.idleTimeout === undefined],
    [
      "Max body size",
      effective.maxBodyBytes > 0 ? formatSize(effective.maxBodyBytes) : "unlimited",
      settings.maxBodyBytes === undefined,
    ],
    [
      "Rate limit",
      `${effective.rateLimit.perSecond}/s, burst ${effective.rateLimit.burst}`,
      settings.rateLimit === undefined,
    ],
    [
      "Capture",
      effective.capture ? "bodies and metadata" : "metadata only",
      settings.capture === undefined,
    ],
  ];
  return (
    <section>
      <h2 className="mb-2 font-medium">Settings</h2>
      <p className="mb-3 text-muted-foreground">Select a call to inspect it.</p>
      <table className="w-full max-w-lg">
        <tbody>
          {rows.map(([name, value, inherited]) => (
            <tr key={name} className="border-b last:border-0">
              <th className="py-1.5 pr-4 text-left font-normal text-muted-foreground">{name}</th>
              <td className="py-1.5">
                {value}
                {inherited && (
                  <span className="ml-2 text-xs text-muted-foreground">relay default</span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
