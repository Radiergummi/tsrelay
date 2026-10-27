import type { Capture } from "@/api/schemas";

export function base64ToBytes(base64: string): Uint8Array {
  const binary = atob(base64);
  const out = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i);
  return out;
}

/** trimPartialRune drops an incomplete UTF-8 sequence at the end of bytes (a capture cut mid-character). */
export function trimPartialRune(bytes: Uint8Array): Uint8Array {
  for (let i = 1; i < 4 && i <= bytes.length; i++) {
    const byte = bytes[bytes.length - i]!;
    if ((byte & 0xc0) === 0x80) continue; // a continuation byte
    const sequenceLength = byte >= 0xf0 ? 4 : byte >= 0xe0 ? 3 : byte >= 0xc0 ? 2 : 1;
    return sequenceLength > i ? bytes.subarray(0, bytes.length - i) : bytes;
  }
  return bytes;
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

export function decodeBody({ body, truncated }: Capture): DecodedBody {
  let bytes = body ? base64ToBytes(body) : new Uint8Array();
  if (truncated) bytes = trimPartialRune(bytes);
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
  const mediaType = (contentType ?? "").split(";")[0]!.trim().toLowerCase();
  if (mediaType === "application/json" || mediaType.endsWith("+json")) return "json";
  if (
    mediaType === "text/html" ||
    mediaType === "application/xhtml+xml" ||
    mediaType.endsWith("/xml") ||
    mediaType.endsWith("+xml")
  )
    return "markup";
  if (mediaType === "application/x-www-form-urlencoded") return "form";
  return "text";
}

export function headerValue(
  headers: Record<string, string[]> | undefined,
  name: string,
): string | undefined {
  const want = name.toLowerCase();
  for (const [key, values] of Object.entries(headers ?? {}))
    if (key.toLowerCase() === want) return values[0];
  return undefined;
}
