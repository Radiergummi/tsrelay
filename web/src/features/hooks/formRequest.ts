import type { FieldError, Hook } from "@/api/schemas";

export type FieldType = "text" | "bool" | "int" | "duration" | "ratelimit" | "time";

/** fieldTypes are the form's fields in order, with how each is sent. */
export const fieldTypes: Record<string, FieldType> = {
  path: "text",
  channel: "text",
  description: "text",
  ephemeral: "bool",
  expiresAt: "time",
  "settings.attachPolicy": "text",
  "settings.hostHeader": "text",
  "settings.holdTimeout": "duration",
  "settings.responseTimeout": "duration",
  "settings.idleTimeout": "duration",
  "settings.maxBodyBytes": "int",
  "settings.rateLimit": "ratelimit",
  "settings.capture": "bool",
};

const durationRE = /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/;

/** fieldValue converts a field's text to its API value: undefined for an empty field, or an error. */
export function fieldValue(raw: string, type: FieldType): { value?: unknown; error?: string } {
  const s = raw.trim();
  if (s === "") return { value: undefined };
  switch (type) {
    case "bool":
      return { value: s === "true" };
    case "int":
      return /^\d+$/.test(s) ? { value: Number(s) } : { error: "must be a whole number of bytes" };
    case "duration":
      return durationRE.test(s)
        ? { value: s }
        : { error: "must be a duration like 30s, 2m or 1h30m" };
    case "ratelimit": {
      const m = /^(\d+(?:\.\d+)?)\s*\/\s*(\d+)$/.exec(s);
      return m
        ? { value: { perSecond: Number(m[1]), burst: Number(m[2]) } }
        : { error: "must be calls per second / burst, like 100/200" };
    }
    default:
      return { value: s };
  }
}

/**
 * formRequest builds the API body from [name, text, type] triples. Dotted
 * names nest. An empty field is left out when creating, so the relay
 * default applies, and sent as null when editing, which resets it.
 */
export function formRequest(
  fields: [string, string, FieldType][],
  editing: boolean,
): { body: Record<string, unknown>; errors: FieldError[] } {
  const body: Record<string, unknown> = {};
  const errors: FieldError[] = [];
  for (const [name, raw, type] of fields) {
    const r = fieldValue(raw, type);
    if (r.error) {
      errors.push({ field: name, message: r.error });
      continue;
    }
    let value = r.value;
    if (value === undefined) {
      if (!editing) continue;
      value = null;
    }
    const keys = name.split(".");
    let obj = body;
    for (const k of keys.slice(0, -1)) obj = (obj[k] ??= {}) as Record<string, unknown>;
    obj[keys.at(-1)!] = value;
  }
  return { body, errors };
}

/** initialValues are the form's starting texts: the hook's own values, with relay defaults left empty. */
export function initialValues(h: Hook | undefined): Record<string, string> {
  const v: Record<string, string> = Object.fromEntries(Object.keys(fieldTypes).map((k) => [k, ""]));
  v["ephemeral"] = "false";
  if (!h) return v;
  const s = h.settings;
  Object.assign(v, {
    path: h.path,
    channel: h.channel,
    description: h.description ?? "",
    ephemeral: String(h.ephemeral),
    expiresAt: h.expiresAt ? new Date(h.expiresAt).toISOString().replace(/\.000Z$/, "Z") : "",
    "settings.attachPolicy": s.attachPolicy ?? "",
    "settings.hostHeader": s.hostHeader ?? "",
    "settings.holdTimeout": s.holdTimeout ?? "",
    "settings.responseTimeout": s.responseTimeout ?? "",
    "settings.idleTimeout": s.idleTimeout ?? "",
    "settings.maxBodyBytes": s.maxBodyBytes === undefined ? "" : String(s.maxBodyBytes),
    "settings.rateLimit": s.rateLimit ? `${s.rateLimit.perSecond}/${s.rateLimit.burst}` : "",
    "settings.capture": s.capture === undefined ? "" : String(s.capture),
  });
  return v;
}

/** parseHeaders turns "Name: value" lines into the API's header object. A line without a colon is a header with an empty value. */
export function parseHeaders(text: string): Record<string, string[]> {
  const out: Record<string, string[]> = {};
  for (const line of text.split("\n")) {
    const t = line.trim();
    if (!t) continue;
    const i = t.indexOf(":");
    const name = (i < 0 ? t : t.slice(0, i)).trim();
    const value = i < 0 ? "" : t.slice(i + 1).trim();
    (out[name] ??= []).push(value);
  }
  return out;
}
