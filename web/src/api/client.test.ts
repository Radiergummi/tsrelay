import { http, HttpResponse } from "msw";
import { expect, test } from "vitest";
import { server } from "@/test/server";
import { ApiError, fieldMessage, formMessage, request, requestVoid, paths } from "./client";
import { hook } from "./schemas";

test("a problem response becomes an ApiError with its field errors", async () => {
  server.use(
    http.post("/hooks", () =>
      HttpResponse.json(
        {
          type: "https://tsrelay.mazetti.me/errors/invalid",
          title: "Unprocessable Entity",
          status: 422,
          detail: "path: must be at most 8 segments",
          code: "invalid",
          errors: [{ field: "path", message: "must be at most 8 segments" }],
        },
        { status: 422, headers: { "Content-Type": "application/problem+json" } },
      ),
    ),
  );
  const err = await request(hook, "POST", paths.hooks(), { path: "a/b/c/d/e/f/g/h/i" }).catch(
    (e) => e,
  );
  expect(err).toBeInstanceOf(ApiError);
  expect(err.status).toBe(422);
  expect(err.code).toBe("invalid");
  expect(fieldMessage(err.fieldErrors, "path")).toBe("must be at most 8 segments");
  expect(err.message).toBe("path: must be at most 8 segments");
});

test("a non-JSON error keeps its text", async () => {
  server.use(http.get("/", () => new HttpResponse("bad gateway\n", { status: 502 })));
  const err = await request(hook, "GET", paths.me).catch((e) => e);
  expect(err).toBeInstanceOf(ApiError);
  expect(err.status).toBe(502);
  expect(err.message).toBe("bad gateway");
});

test("an unreachable relay is an ApiError with status 0", async () => {
  server.use(http.get("/", () => HttpResponse.error()));
  const err = await request(hook, "GET", paths.me).catch((e) => e);
  expect(err).toBeInstanceOf(ApiError);
  expect(err.status).toBe(0);
  expect(err.message).toMatch(/not reachable/);
});

test("requestVoid accepts 204, and paths escape IDs", async () => {
  server.use(http.delete("/hooks/:id", () => new HttpResponse(null, { status: 204 })));
  await expect(requestVoid("DELETE", paths.hook("h_1"))).resolves.toBeUndefined();
  expect(paths.call("a/b", "c?d")).toBe("/hooks/a%2Fb/calls/c%3Fd");
});

test("formMessage is the message only when some field error has no slot of its own", () => {
  const unprocessable = (fields: string[]) =>
    new ApiError({
      status: 422,
      code: "invalid",
      title: "Unprocessable Entity",
      detail: "it went wrong",
      fieldErrors: fields.map((field) => ({ field, message: `${field} is wrong` })),
    });
  const slots = (f: string) => f === "path";
  expect(formMessage(unprocessable(["path"]), slots)).toBe("");
  expect(formMessage(unprocessable(["path", "ttl"]), slots)).toBe("it went wrong");
  expect(formMessage(unprocessable([]), slots)).toBe("it went wrong");
});

test("a bodyless write still declares JSON", async () => {
  let type: string | null = null;
  server.use(
    http.post("/hooks/h_1/rotate-public-url", ({ request: r }) => {
      type = r.headers.get("Content-Type");
      return new HttpResponse(null, { status: 204 });
    }),
  );
  await requestVoid("POST", paths.rotatePublicUrl("h_1"));
  expect(type).toBe("application/json");
});
