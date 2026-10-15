import { http, HttpResponse } from "msw";
import { expect, test } from "vitest";
import { fixtures } from "@/test/fixtures";
import { server } from "@/test/server";
import { ApiError, fieldMessage, request, requestMe, requestVoid, paths } from "./client";
import { hook } from "./schemas";

test("a problem response becomes an ApiError with its field errors", async () => {
  server.use(
    http.post("/api/v1/hooks", () =>
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
  server.use(http.get("/api/v1/me", () => new HttpResponse("bad gateway\n", { status: 502 })));
  const err = await request(hook, "GET", paths.me).catch((e) => e);
  expect(err).toBeInstanceOf(ApiError);
  expect(err.status).toBe(502);
  expect(err.message).toBe("bad gateway");
});

test("an unreachable relay is an ApiError with status 0", async () => {
  server.use(http.get("/api/v1/me", () => HttpResponse.error()));
  const err = await request(hook, "GET", paths.me).catch((e) => e);
  expect(err).toBeInstanceOf(ApiError);
  expect(err.status).toBe(0);
  expect(err.message).toMatch(/not reachable/);
});

test("requestVoid accepts 204, and paths escape IDs", async () => {
  server.use(http.delete("/api/v1/hooks/:id", () => new HttpResponse(null, { status: 204 })));
  await expect(requestVoid("DELETE", paths.hook("h_1"))).resolves.toBeUndefined();
  expect(paths.call("a/b", "c?d")).toBe("/api/v1/hooks/a%2Fb/calls/c%3Fd");
});

test("requestMe carries the relay's version from its header", async () => {
  server.use(
    http.get("/api/v1/me", () =>
      HttpResponse.json(fixtures.me, { headers: { "Tsrelay-Version": "v0.4.1" } }),
    ),
  );
  expect((await requestMe()).version).toBe("v0.4.1");
});

test("requestMe treats a relay without a version header as dev", async () => {
  server.use(http.get("/api/v1/me", () => HttpResponse.json(fixtures.me)));
  expect((await requestMe()).version).toBe("dev");
});
