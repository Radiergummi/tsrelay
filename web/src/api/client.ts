import * as z from "zod/mini";
import { fieldError, problem, type FieldError } from "./schemas";

/** ApiError is a failed API request: a problem response, or no response at all (status 0). */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly title: string;
  readonly detail: string;
  readonly fieldErrors: FieldError[];

  constructor({
    status,
    code,
    title,
    detail,
    fieldErrors,
  }: {
    status: number;
    code: string;
    title: string;
    detail: string;
    fieldErrors: FieldError[];
  }) {
    super(detail || title || `The relay answered ${status}.`);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.title = title;
    this.detail = detail;
    this.fieldErrors = fieldErrors;
  }
}

/** fieldMessage is the message for one field, or for the whole form when name is "". */
export const fieldMessage = (errors: FieldError[], name: string): string | undefined =>
  errors.find(({ field }) => field === name)?.message;

/**
 * formMessage is a 422's form-level message: its message when some field
 * error has no slot of its own (shown is false), or there are none at all.
 */
export function formMessage(error: ApiError, shown: (field: string) => boolean): string {
  const rest = error.fieldErrors.filter(({ field }) => !shown(field));
  return rest.length || !error.fieldErrors.length ? error.message : "";
}

const id = encodeURIComponent;

/**
 * paths are the resources' URLs, the same as the app's own routes: the
 * relay answers JSON when asked for it (send sets Accept), and events when
 * an EventSource asks. IDs come from the API and are escaped anyway.
 */
export const paths = {
  me: "/",
  hooks: (all = false, channel?: string) => "/hooks" + query({ all, channel }),
  hook: (hookId: string) => `/hooks/${id(hookId)}`,
  rotatePublicUrl: (hookId: string) => `/hooks/${id(hookId)}/rotate-public-url`,
  calls: (hookId: string) => `/hooks/${id(hookId)}/calls`,
  call: (hookId: string, callId: string) => `/hooks/${id(hookId)}/calls/${id(callId)}`,
  replay: (hookId: string, callId: string) => `/hooks/${id(hookId)}/calls/${id(callId)}/replay`,
  test: (hookId: string) => `/hooks/${id(hookId)}/test`,
  licenses: "/licenses",
  licenseText: (textId: string) => `/licenses/${id(textId)}`,
  notices: "/notices.txt",
  channels: (all = false) => "/channels" + query({ all }),
  channel: (name: string, all = false) => `/channels/${id(name)}` + query({ all }),
};

/** query is "?all=true&channel=…" for the options that are set, or "". */
function query({ all, channel }: { all: boolean; channel?: string | undefined }): string {
  const params = new URLSearchParams();
  if (all) params.set("all", "true");
  if (channel) params.set("channel", channel);
  const encoded = params.toString();
  return encoded ? `?${encoded}` : "";
}

async function send(
  method: string,
  path: string,
  body: unknown,
  signal: AbortSignal | undefined,
): Promise<Response> {
  const headers: Record<string, string> = { Accept: "application/json" };
  const init: RequestInit = { method, headers, credentials: "same-origin" };
  if (signal) init.signal = signal;
  // Bodyless writes too: the relay's guard requires JSON on every write.
  if (method !== "GET") headers["Content-Type"] = "application/json";
  if (body !== undefined) {
    init.body = JSON.stringify(body);
  }
  let response: Response;
  try {
    response = await fetch(path, init);
  } catch (error) {
    if (signal?.aborted) throw error;
    const reason = error instanceof Error ? error.message : String(error);
    throw new ApiError({
      status: 0,
      code: "unreachable",
      title: "",
      detail: `The relay is not reachable: ${reason}`,
      fieldErrors: [],
    });
  }
  if (!response.ok) throw await toApiError(response);
  return response;
}

async function toApiError(response: Response): Promise<ApiError> {
  const text = await response.text();
  let body: z.infer<typeof problem> = {};
  try {
    const parsed = problem.safeParse(JSON.parse(text));
    if (parsed.success) body = parsed.data;
  } catch {
    body = { detail: text.trim() };
  }
  const errors = Array.isArray(body.errors)
    ? body.errors.flatMap((entry) => {
        const parsed = fieldError.safeParse(entry);
        return parsed.success ? [parsed.data] : [];
      })
    : [];
  return new ApiError({
    status: response.status,
    code: body.code ?? "",
    title: body.title ?? response.statusText,
    detail: body.detail ?? "",
    fieldErrors: errors,
  });
}

/** request sends a JSON request and parses the answer with schema. */
export async function request<T>(
  schema: z.ZodMiniType<T>,
  method: string,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
): Promise<T> {
  const response = await send(method, path, body, signal);
  return schema.parse(await response.json());
}

/** requestText sends a GET request and returns the answer as text. */
export async function requestText(path: string, signal?: AbortSignal): Promise<string> {
  const response = await send("GET", path, undefined, signal);
  return response.text();
}

/** requestVoid sends a request whose answer has no body (204). */
export async function requestVoid(
  method: string,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
): Promise<void> {
  await send(method, path, body, signal);
}
