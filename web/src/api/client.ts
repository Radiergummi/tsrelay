import * as z from "zod/mini";
import { fieldError, problem, type FieldError } from "./schemas";

/** ApiError is a failed API request: a problem response, or no response at all (status 0). */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly title: string;
  readonly detail: string;
  readonly fieldErrors: FieldError[];

  constructor(init: {
    status: number;
    code: string;
    title: string;
    detail: string;
    fieldErrors: FieldError[];
  }) {
    super(init.detail || init.title || `The relay answered ${init.status}.`);
    this.name = "ApiError";
    this.status = init.status;
    this.code = init.code;
    this.title = init.title;
    this.detail = init.detail;
    this.fieldErrors = init.fieldErrors;
  }
}

/** fieldMessage is the message for one field, or for the whole form when name is "". */
export const fieldMessage = (errors: FieldError[], name: string): string | undefined =>
  errors.find((e) => e.field === name)?.message;

/**
 * formMessage is a 422's form-level message: its message when some field
 * error has no slot of its own (shown is false), or there are none at all.
 */
export function formMessage(err: ApiError, shown: (field: string) => boolean): string {
  const rest = err.fieldErrors.filter((e) => !shown(e.field));
  return rest.length || !err.fieldErrors.length ? err.message : "";
}

const id = encodeURIComponent;

/**
 * paths are the resources' URLs, the same as the app's own routes: the
 * relay answers JSON when asked for it (send sets Accept), and events when
 * an EventSource asks. IDs come from the API and are escaped anyway.
 */
export const paths = {
  me: "/",
  hooks: (all = false) => (all ? "/hooks?all=true" : "/hooks"),
  hook: (hookId: string) => `/hooks/${id(hookId)}`,
  calls: (hookId: string) => `/hooks/${id(hookId)}/calls`,
  call: (hookId: string, callId: string) => `/hooks/${id(hookId)}/calls/${id(callId)}`,
  replay: (hookId: string, callId: string) => `/hooks/${id(hookId)}/calls/${id(callId)}/replay`,
  test: (hookId: string) => `/hooks/${id(hookId)}/test`,
  licenses: "/licenses",
  licenseText: (textId: string) => `/licenses/${id(textId)}`,
  notices: "/notices.txt",
};

async function send(
  method: string,
  path: string,
  body: unknown,
  signal: AbortSignal | undefined,
): Promise<Response> {
  const headers: Record<string, string> = { Accept: "application/json" };
  const init: RequestInit = { method, headers, credentials: "same-origin" };
  if (signal) init.signal = signal;
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(body);
  }
  let resp: Response;
  try {
    resp = await fetch(path, init);
  } catch (err) {
    if (signal?.aborted) throw err;
    const reason = err instanceof Error ? err.message : String(err);
    throw new ApiError({
      status: 0,
      code: "unreachable",
      title: "",
      detail: `The relay is not reachable: ${reason}`,
      fieldErrors: [],
    });
  }
  if (!resp.ok) throw await toApiError(resp);
  return resp;
}

async function toApiError(resp: Response): Promise<ApiError> {
  const text = await resp.text();
  let p: z.infer<typeof problem> = {};
  try {
    const parsed = problem.safeParse(JSON.parse(text));
    if (parsed.success) p = parsed.data;
  } catch {
    p = { detail: text.trim() };
  }
  const errors = Array.isArray(p.errors)
    ? p.errors.flatMap((e) => (fieldError.safeParse(e).success ? [e as FieldError] : []))
    : [];
  return new ApiError({
    status: resp.status,
    code: p.code ?? "",
    title: p.title ?? resp.statusText,
    detail: p.detail ?? "",
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
  const resp = await send(method, path, body, signal);
  return schema.parse(await resp.json());
}

/** requestText sends a GET request and returns the answer as text. */
export async function requestText(path: string, signal?: AbortSignal): Promise<string> {
  const resp = await send("GET", path, undefined, signal);
  return resp.text();
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
