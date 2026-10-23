import * as z from "zod/mini";

// Every schema mirrors the Go JSON in internal/api, internal/inspect and
// internal/hooks. Setting durations are Go duration strings ("1.5s", "2m0s"),
// measured durations (…Ms) numbers of milliseconds, times RFC 3339, and
// bodies base64.

const headers = z.record(z.string(), z.array(z.string()));
const duration = z.string();
const millis = z.number();

export const capture = z.object({
  body: z.optional(z.string()),
  size: z.number(),
  truncated: z.optional(z.boolean()),
  captured: z.boolean(),
});

export const sender = z.object({
  node: z.optional(z.string()),
  tags: z.optional(z.array(z.string())),
  login: z.optional(z.string()),
  ip: z.optional(z.string()),
  public: z.optional(z.boolean()),
});

export const summary = z.object({
  id: z.string(),
  hookId: z.string(),
  receivedAt: z.string(),
  kind: z.string(), // "sender" | "replay" | "test" | "public"
  replayOf: z.optional(z.string()),
  refused: z.optional(z.boolean()), // the sender may not use the hook's channel
  sender,
  method: z.string(),
  path: z.string(),
  outcome: z.string(), // "ok", "pending", or a relay error code
  status: z.optional(z.number()),
  durationMs: millis,
});
export const summaries = z.array(summary);
/** callList is GET /hooks/{id}/calls. */
export const callList = z.object({ calls: summaries });

export const attempt = z.object({
  id: z.string(),
  startedAt: z.string(),
  tunnelId: z.optional(z.string()),
  node: z.optional(z.string()),
  waitedForMs: millis,
  ttfbMs: millis,
  durationMs: millis,
  status: z.optional(z.number()),
  responseHeaders: z.optional(headers),
  redacted: z.optional(z.array(z.string())),
  responseBody: capture,
  bytesIn: z.number(),
  bytesOut: z.number(),
  error: z.optional(z.string()),
});

export const call = z.extend(summary, {
  query: z.optional(z.string()),
  queryTruncated: z.optional(z.boolean()),
  requestHeaders: z.optional(headers),
  redacted: z.optional(z.array(z.string())),
  requestBody: capture,
  attempts: z.array(attempt),
  replay: z.optional(z.object({ allowed: z.boolean(), reason: z.string() })),
});

const rateLimit = z.object({ perSecond: z.number(), burst: z.number() });

export const effective = z.object({
  attachPolicy: z.string(),
  hostHeader: z.string(),
  holdTimeout: duration,
  responseTimeout: duration,
  idleTimeout: duration,
  maxBodyBytes: z.number(),
  rateLimit,
  capture: z.boolean(),
});

export const settings = z.object({
  attachPolicy: z.optional(z.string()),
  hostHeader: z.optional(z.string()),
  holdTimeout: z.optional(duration),
  responseTimeout: z.optional(duration),
  idleTimeout: z.optional(duration),
  maxBodyBytes: z.optional(z.number()),
  rateLimit: z.optional(rateLimit),
  capture: z.optional(z.boolean()),
});

export const maxima = z.object({
  holdTimeout: z.optional(duration),
  responseTimeout: z.optional(duration),
  idleTimeout: z.optional(duration),
  maxBodyBytes: z.optional(z.number()),
  rateLimit: z.optional(rateLimit),
});

export const hook = z.object({
  id: z.string(),
  url: z.string(),
  owner: z.string(),
  path: z.string(),
  channel: z.string(),
  description: z.optional(z.string()),
  ephemeral: z.boolean(),
  public: z.optional(z.boolean()),
  publicUrl: z.optional(z.string()),
  expiresAt: z.optional(z.string()),
  settings,
  effective,
  createdAt: z.string(),
  tunnel: z.optional(
    z.object({ id: z.string(), node: z.string(), target: z.optional(z.string()) }),
  ),
  lastCall: z.optional(summary),
});
export const hooks = z.array(hook);
/** hookList is GET /hooks; the object leaves room for more than the list. */
export const hookList = z.object({ hooks });

export const channel = z.object({
  name: z.string(),
  hookCount: z.number(),
  hooks: z.optional(hooks), // the viewer's; left out for an admin's ?all=true
  receive: z.array(z.string()),
});

export const channels = z.object({
  receive: z.array(z.string()),
  send: z.array(z.string()),
  channels: z.array(channel),
});

export const channelDetail = z.object({
  name: z.string(),
  canReceive: z.boolean(),
  canSend: z.boolean(),
  hooks,
});

export const me = z.object({
  login: z.optional(z.string()),
  node: z.string(),
  tags: z.optional(z.array(z.string())),
  slug: z.optional(z.string()),
  roles: z.record(z.string(), z.nullable(z.array(z.string()))),
  limits: z.object({
    maxHooks: z.number(),
    maxTunnels: z.number(),
    maxHookTtl: z.string(), // "0s" means no limit
  }),
  hookCount: z.number(),
  capName: z.string(),
  version: z.string(),
  commit: z.optional(z.string()),
  policy: z.object({
    defaults: effective,
    max: maxima,
    callsPerHook: z.number(),
    noBodies: z.optional(z.boolean()),
  }),
  publicPatterns: z.array(z.string()),
  funnel: z.optional(z.boolean()),
  features: z.array(z.string()),
});

export const licenseEntry = z.object({
  id: z.optional(z.string()), // an SPDX identifier or expression
  name: z.optional(z.string()),
  url: z.optional(z.string()),
});

export const licenseComponent = z.object({
  name: z.string(),
  version: z.optional(z.string()),
  description: z.optional(z.string()),
  ecosystem: z.string(), // "go" | "npm" | "other"
  licenses: z.array(licenseEntry),
  homepage: z.optional(z.string()),
  repository: z.optional(z.string()),
  textId: z.optional(z.string()),
  noticeId: z.optional(z.string()),
});

export const licenses = z.object({ components: z.array(licenseComponent) });

export const fieldError = z.object({ field: z.string(), message: z.string() });

export const problem = z.object({
  type: z.optional(z.string()),
  title: z.optional(z.string()),
  status: z.optional(z.number()),
  detail: z.optional(z.string()),
  code: z.optional(z.string()),
  errors: z.optional(z.unknown()),
});

export const hookEvent = z.object({ id: z.string() });

export type Capture = z.infer<typeof capture>;
export type Sender = z.infer<typeof sender>;
export type Summary = z.infer<typeof summary>;
export type Attempt = z.infer<typeof attempt>;
export type Call = z.infer<typeof call>;
export type Effective = z.infer<typeof effective>;
export type Settings = z.infer<typeof settings>;
export type Maxima = z.infer<typeof maxima>;
export type Hook = z.infer<typeof hook>;
export type Channel = z.infer<typeof channel>;
export type Channels = z.infer<typeof channels>;
export type ChannelDetail = z.infer<typeof channelDetail>;
export type Me = z.infer<typeof me>;
export type LicenseEntry = z.infer<typeof licenseEntry>;
export type LicenseComponent = z.infer<typeof licenseComponent>;
export type FieldError = z.infer<typeof fieldError>;

/** HookRequest is the body of POST and PATCH /hooks (hooks.CreateRequest and UpdateRequest). */
export type HookRequest = Record<string, unknown>;

/** TestRequest is the body of POST /hooks/{id}/test (api.TestRequest). */
export interface TestRequest {
  method: string;
  path: string;
  headers: Record<string, string[]>;
  body: string;
}
