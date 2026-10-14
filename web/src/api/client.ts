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

  /** fieldError is the message for one field, or for the whole form when name is "". */
  fieldError(name: string): string | undefined {
    return this.fieldErrors.find((e) => e.field === name)?.message;
  }
}

const id = encodeURIComponent;

/** paths are the API's URLs. IDs come from the API and are escaped anyway. */
export const paths = {
  me: "/api/v1/me",
  hooks: (all = false) => (all ? "/api/v1/hooks?all=true" : "/api/v1/hooks"),
  hook: (hookId: string) => `/api/v1/hooks/${id(hookId)}`,
  calls: (hookId: string) => `/api/v1/hooks/${id(hookId)}/calls`,
  call: (hookId: string, callId: string) => `/api/v1/hooks/${id(hookId)}/calls/${id(callId)}`,
  replay: (hookId: string, callId: string) =>
    `/api/v1/hooks/${id(hookId)}/calls/${id(callId)}/replay`,
  test: (hookId: string) => `/api/v1/hooks/${id(hookId)}/test`,
  events: (all: boolean) => (all ? "/api/v1/events?all=true" : "/api/v1/events"),
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

/** requestVoid sends a request whose answer has no body (204). */
export async function requestVoid(
  method: string,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
): Promise<void> {
  await send(method, path, body, signal);
}
