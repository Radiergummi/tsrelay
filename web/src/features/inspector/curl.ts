import type { Call, Hook } from "@/api/schemas";
import type { DecodedBody } from "./bytes";

/** Headers curl sets itself. */
const curlSets = new Set(["Content-Length", "Connection", "Transfer-Encoding"]);

/**
 * shellQuote quotes s for a POSIX shell: single quotes keep every byte
 * literal, and a single quote inside is closed, escaped and reopened. A
 * carriage return becomes a command substitution that has printf produce
 * it at run time, so the command never holds a raw CR (clipboards and
 * terminals turn those into line feeds).
 */
export function shellQuote(s: string): string {
  let out = "'";
  for (const ch of s) {
    if (ch === "'") out += `'"'"'`;
    else if (ch === "\r") out += `'"$(printf '\\r')"'`;
    else out += ch;
  }
  return out + "'";
}

/** headerLines lists h one value per line, sorted by name. */
export function headerLines(h: Record<string, string[]> | undefined): [string, string][] {
  return Object.keys(h ?? {})
    .sort()
    .flatMap((name) => h![name]!.map((v): [string, string] => [name, v]));
}

/** printfEscape writes bytes for printf's %b: printable ASCII as it is, everything else as \0ooo. */
function printfEscape(bytes: Uint8Array): string {
  let out = "";
  for (const b of bytes) {
    if (b === 0x5c) out += "\\\\";
    else if (b >= 0x20 && b < 0x7f) out += String.fromCharCode(b);
    else out += "\\0" + b.toString(8).padStart(3, "0");
  }
  return out;
}

/**
 * curlCommand renders a curl command that sends method, headers and body
 * to url (DESIGN.md §9.3). -g keeps curl from reading [...] and {...} in
 * the URL as globs. A body a shell argument can't hold (NUL bytes, or not
 * UTF-8) is piped in through printf instead, byte for byte.
 */
export function curlCommand(
  method: string,
  url: string,
  headers: Record<string, string[]> | undefined,
  body: DecodedBody,
): string {
  const pipe = body.bytes.length > 0 && body.binary;
  let cmd =
    (pipe ? `printf '%b' ${shellQuote(printfEscape(body.bytes))} | ` : "") +
    `curl -g -X ${shellQuote(method)} ${shellQuote(url)}`;
  if (Object.keys(headers ?? {}).some((k) => k.toLowerCase() === "accept-encoding"))
    cmd += " --compressed";
  for (const [name, value] of headerLines(headers)) {
    if (!curlSets.has(name)) cmd += ` \\\n  -H ${shellQuote(`${name}: ${value}`)}`;
  }
  if (pipe) cmd += " \\\n  --data-binary @-";
  else if (body.bytes.length > 0) cmd += ` \\\n  --data-binary ${shellQuote(body.text!)}`;
  return cmd;
}

/**
 * curlTargets are the copy-as-curl commands for a call: to the tunnel's
 * local target, which developers usually want, and to the hook's URL,
 * which only nodes with the sender role may call.
 */
export function curlTargets(
  hook: Hook,
  call: Call,
  body: DecodedBody,
): { label: string; command: string }[] {
  const rest = call.path + (call.query ? `?${call.query}` : "");
  const out: { label: string; command: string }[] = [];
  if (hook.tunnel?.target) {
    out.push({
      label: "local target",
      command: curlCommand(
        call.method,
        hook.tunnel.target.replace(/\/$/, "") + rest,
        call.requestHeaders,
        body,
      ),
    });
  }
  out.push({
    label: "hook URL",
    command: curlCommand(call.method, hook.url + rest, call.requestHeaders, body),
  });
  return out;
}
