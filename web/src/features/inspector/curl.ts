import type { Call, Hook } from "@/api/schemas";
import type { DecodedBody } from "./bytes";

/** Headers curl sets itself. */
const curlSets = new Set(["Content-Length", "Connection", "Transfer-Encoding"]);

/**
 * shellQuote quotes text for a POSIX shell: single quotes keep every byte
 * literal, and a single quote inside is closed, escaped and reopened. A
 * carriage return becomes a command substitution that has printf produce
 * it at run time, so the command never holds a raw CR (clipboards and
 * terminals turn those into line feeds).
 */
export function shellQuote(text: string): string {
  let out = "'";
  for (const character of text) {
    if (character === "'") out += `'"'"'`;
    else if (character === "\r") out += `'"$(printf '\\r')"'`;
    else out += character;
  }
  return out + "'";
}

/** headerLines lists headers one value per line, sorted by name. */
export function headerLines(headers: Record<string, string[]> | undefined): [string, string][] {
  return Object.keys(headers ?? {})
    .sort()
    .flatMap((name) => headers![name]!.map((value): [string, string] => [name, value]));
}

/** printfEscape writes bytes for printf's %b: printable ASCII as it is, everything else as \0ooo. */
function printfEscape(bytes: Uint8Array): string {
  let out = "";
  for (const byte of bytes) {
    if (byte === 0x5c) out += "\\\\";
    else if (byte >= 0x20 && byte < 0x7f) out += String.fromCharCode(byte);
    else out += "\\0" + byte.toString(8).padStart(3, "0");
  }
  return out;
}

/** terminalControl matches the control characters shellQuote can't keep out of a pasted command. */
// eslint-disable-next-line no-control-regex -- matching these control characters is the point.
const terminalControl = /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f]/;

/**
 * curlCommand renders a curl command that sends method, headers and body
 * to url (DESIGN.md §9.3). -g keeps curl from reading [...] and {...} in
 * the URL as globs. A body a shell argument can't hold (NUL bytes, or not
 * UTF-8) is piped in through printf instead, byte for byte, and so is one
 * with control characters a terminal would act on when the command is
 * pasted (C0 other than tab, LF and CR, DEL, and C1): the command never
 * holds them raw.
 */
export function curlCommand(
  method: string,
  url: string,
  headers: Record<string, string[]> | undefined,
  body: DecodedBody,
): string {
  const { bytes, text, binary } = body;
  const pipe = bytes.length > 0 && (binary || terminalControl.test(text!));
  let command =
    (pipe ? `printf '%b' ${shellQuote(printfEscape(bytes))} | ` : "") +
    `curl -g -X ${shellQuote(method)} ${shellQuote(url)}`;
  if (Object.keys(headers ?? {}).some((name) => name.toLowerCase() === "accept-encoding"))
    command += " --compressed";
  for (const [name, value] of headerLines(headers)) {
    if (!curlSets.has(name)) command += ` \\\n  -H ${shellQuote(`${name}: ${value}`)}`;
  }
  if (pipe) command += " \\\n  --data-binary @-";
  else if (bytes.length > 0) command += ` \\\n  --data-binary ${shellQuote(text!)}`;
  return command;
}

/**
 * curlTargets are the copy-as-curl commands for a call: to the tunnel's
 * local target, which developers usually want, and to the hook's URL,
 * which only nodes with the sender role may call.
 */
export function curlTargets(
  hook: Pick<Hook, "url" | "tunnel">,
  call: Call,
  body: DecodedBody,
): { label: string; command: string }[] {
  const { method, path, query, requestHeaders } = call;
  const rest = path + (query ? `?${query}` : "");
  const out: { label: string; command: string }[] = [];
  const target = hook.tunnel?.target;
  if (target) {
    out.push({
      label: "local target",
      command: curlCommand(method, target.replace(/\/$/, "") + rest, requestHeaders, body),
    });
  }
  out.push({
    label: "hook URL",
    command: curlCommand(method, hook.url + rest, requestHeaders, body),
  });
  return out;
}
