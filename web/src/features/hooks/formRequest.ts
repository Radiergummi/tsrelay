import type { Effective, FieldError, Hook, HookRequest } from "@/api/schemas";
import { parseDuration } from "@/lib/duration";

export type FieldType = "text" | "bool" | "int" | "duration" | "ratelimit" | "time";

/** fieldTypes are the form's fields in order, with how each is sent. */
export const fieldTypes = {
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
} as const satisfies Record<string, FieldType>;

/** FieldName is one of the form's fields; a dotted name nests in the request. */
export type FieldName = keyof typeof fieldTypes;

/** FormValues are the form's texts, per field. */
export type FormValues = Record<FieldName, string>;

/** fieldValue converts a field's text to its API value: undefined for an empty field, or an error. */
export function fieldValue(raw: string, type: FieldType): { value?: unknown; error?: string } {
  const text = raw.trim();
  if (text === "") return { value: undefined };
  switch (type) {
    case "bool":
      return { value: text === "true" };
    case "int":
      return /^\d+$/.test(text)
        ? { value: Number(text) }
        : { error: "must be a whole number of bytes" };
    case "duration":
      return parseDuration(text) !== null
        ? { value: text }
        : { error: "must be a duration like 30s, 2m or 1h30m" };
    case "ratelimit": {
      const match = /^(\d+(?:\.\d+)?)\s*\/\s*(\d+)$/.exec(text);
      return match
        ? { value: { perSecond: Number(match[1]), burst: Number(match[2]) } }
        : { error: "needs calls per second and a whole-number burst" };
    }
    default:
      return { value: text };
  }
}

/**
 * formRequest builds the API body from the fields' texts, in form order.
 * Dotted names nest. An empty field is left out when creating, so the relay
 * default applies. Editing (initial holds the form's starting texts) sends
 * only the fields that changed, as the PATCH is partial: a cleared one as
 * null, which resets it.
 */
export function formRequest(
  values: Partial<FormValues>,
  initial?: FormValues,
): { body: HookRequest; errors: FieldError[] } {
  const body: Record<string, unknown> = {};
  const errors: FieldError[] = [];
  for (const name of Object.keys(fieldTypes) as FieldName[]) {
    const raw = values[name];
    if (raw === undefined || (initial && raw.trim() === initial[name].trim())) continue;
    const { value: converted, error } = fieldValue(raw, fieldTypes[name]);
    if (error) {
      errors.push({ field: name, message: error });
      continue;
    }
    let value = converted;
    if (value === undefined) {
      if (!initial) continue;
      value = null;
    }
    const keys = name.split(".");
    let parent = body;
    for (const key of keys.slice(0, -1)) parent = (parent[key] ??= {}) as Record<string, unknown>;
    parent[keys.at(-1)!] = value;
  }
  // The dotted names nest exactly as HookRequest does.
  return { body: body as HookRequest, errors };
}

/** initialValues are the form's starting texts: the hook's own values, with relay defaults left empty. */
export function initialValues(hook: Hook | undefined): FormValues {
  const {
    attachPolicy,
    hostHeader,
    holdTimeout,
    responseTimeout,
    idleTimeout,
    maxBodyBytes,
    rateLimit,
    capture,
  } = hook?.settings ?? {};
  return {
    path: hook?.path ?? "",
    channel: hook?.channel ?? "",
    description: hook?.description ?? "",
    ephemeral: String(hook?.ephemeral ?? false),
    public: String(hook?.public ?? false),
    expiresAt: hook?.expiresAt
      ? new Date(hook.expiresAt).toISOString().replace(/\.000Z$/, "Z")
      : "",
    "settings.attachPolicy": attachPolicy ?? "",
    "settings.hostHeader": hostHeader ?? "",
    "settings.holdTimeout": holdTimeout ?? "",
    "settings.responseTimeout": responseTimeout ?? "",
    "settings.idleTimeout": idleTimeout ?? "",
    "settings.maxBodyBytes": maxBodyBytes === undefined ? "" : String(maxBodyBytes),
    "settings.rateLimit": rateLimit ? `${rateLimit.perSecond}/${rateLimit.burst}` : "",
    "settings.capture": capture === undefined ? "" : String(capture),
  };
}

/** effectiveSettings are the delivery settings values would give: each valid override, else the relay default. */
export function effectiveSettings(values: FormValues, defaults: Effective): Effective {
  const effective: Record<string, unknown> = { ...defaults };
  for (const setting of Object.keys(defaults) as (keyof Effective)[]) {
    const { value } = fieldValue(values[`settings.${setting}`], fieldTypes[`settings.${setting}`]);
    if (value !== undefined) effective[setting] = value;
  }
  return effective as Effective;
}

/** parseHeaders turns "Name: value" lines into the API's header object. A line without a colon is a header with an empty value. */
export function parseHeaders(text: string): Record<string, string[]> {
  const headers: Record<string, string[]> = {};
  for (const line of text.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const colon = trimmed.indexOf(":");
    const name = (colon < 0 ? trimmed : trimmed.slice(0, colon)).trim();
    const value = colon < 0 ? "" : trimmed.slice(colon + 1).trim();
    (headers[name] ??= []).push(value);
  }
  return headers;
}
