import type { Capture } from "@/api/schemas";

export function base64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

/** trimPartialRune drops an incomplete UTF-8 sequence at the end of b (a capture cut mid-character). */
export function trimPartialRune(b: Uint8Array): Uint8Array {
  for (let i = 1; i < 4 && i <= b.length; i++) {
    const byte = b[b.length - i]!;
    if ((byte & 0xc0) === 0x80) continue; // a continuation byte
    const need = byte >= 0xf0 ? 4 : byte >= 0xe0 ? 3 : byte >= 0xc0 ? 2 : 1;
    return need > i ? b.subarray(0, b.length - i) : b;
  }
  return b;
}

export interface DecodedBody {
  bytes: Uint8Array;
  /** The bytes as text, if they are valid UTF-8 (NULs included). */
  text: string | null;
  hasNul: boolean;
  /** Not text, or text a shell argument can't hold. */
  binary: boolean;
}

const utf8 = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true });

export function decodeBody(c: Capture): DecodedBody {
  let bytes = c.body ? base64ToBytes(c.body) : new Uint8Array();
  if (c.truncated) bytes = trimPartialRune(bytes);
  let text: string | null;
  try {
    text = utf8.decode(bytes);
  } catch {
    text = null;
  }
  const hasNul = bytes.includes(0);
  return { bytes, text, hasNul, binary: text === null || hasNul };
}

export type BodyKind = "json" | "markup" | "form" | "text";

export function bodyKind(contentType: string | undefined): BodyKind {
  const mt = (contentType ?? "").split(";")[0]!.trim().toLowerCase();
  if (mt === "application/json" || mt.endsWith("+json")) return "json";
  if (
    mt === "text/html" ||
    mt === "application/xhtml+xml" ||
    mt.endsWith("/xml") ||
    mt.endsWith("+xml")
  )
    return "markup";
  if (mt === "application/x-www-form-urlencoded") return "form";
  return "text";
}

export function headerValue(
  h: Record<string, string[]> | undefined,
  name: string,
): string | undefined {
  const want = name.toLowerCase();
  for (const [k, v] of Object.entries(h ?? {})) if (k.toLowerCase() === want) return v[0];
  return undefined;
}
