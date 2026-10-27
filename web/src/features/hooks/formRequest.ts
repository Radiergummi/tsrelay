import type { Effective, FieldError, Hook } from "@/api/schemas";
import { parseDuration } from "@/lib/duration";

export type FieldType = "text" | "bool" | "int" | "duration" | "ratelimit" | "time";

/** fieldTypes are the form's fields in order, with how each is sent. */
export const fieldTypes: Record<string, FieldType> = {
  path: "text",
  channel: "text",
  description: "text",
  ephemeral: "bool",
  public: "bool",
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
      return parseDuration(s) !== null
        ? { value: s }
        : { error: "must be a duration like 30s, 2m or 1h30m" };
    case "ratelimit": {
      const m = /^(\d+(?:\.\d+)?)\s*\/\s*(\d+)$/.exec(s);
      return m
        ? { value: { perSecond: Number(m[1]), burst: Number(m[2]) } }
        : { error: "needs calls per second and a whole-number burst" };
    }
    default:
      return { value: s };
  }
}

/**
 * formRequest builds the API body from [name, text, type] triples. Dotted
 * names nest. An empty field is left out when creating, so the relay
 * default applies. Editing (initial holds the form's starting texts) sends
 * only the fields that changed, as the PATCH is partial: a cleared one as
 * null, which resets it.
 */
export function formRequest(
  fields: [string, string, FieldType][],
  initial?: Record<string, string>,
): { body: Record<string, unknown>; errors: FieldError[] } {
  const body: Record<string, unknown> = {};
  const errors: FieldError[] = [];
  for (const [name, raw, type] of fields) {
    if (initial && raw.trim() === initial[name]?.trim()) continue;
    const r = fieldValue(raw, type);
    if (r.error) {
      errors.push({ field: name, message: r.error });
      continue;
    }
    let value = r.value;
    if (value === undefined) {
      if (!initial) continue;
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
  v["public"] = "false";
  if (!h) return v;
  const s = h.settings;
  Object.assign(v, {
    path: h.path,
    channel: h.channel,
    description: h.description ?? "",
    ephemeral: String(h.ephemeral),
    public: String(h.public ?? false),
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

/** effectiveSettings are the delivery settings values would give: each valid override, else the relay default. */
export function effectiveSettings(values: Record<string, string>, defaults: Effective): Effective {
  const e: Record<string, unknown> = { ...defaults };
  for (const k of Object.keys(defaults)) {
    const r = fieldValue(values[`settings.${k}`] ?? "", fieldTypes[`settings.${k}`]!);
    if (r.value !== undefined) e[k] = r.value;
  }
  return e as Effective;
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
