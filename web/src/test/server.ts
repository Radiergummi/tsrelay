import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { fixtures } from "./fixtures";

const byId = (list: { id: string }[], id: unknown) => list.find((x) => x.id === id);
const calls = [
  fixtures.callJson,
  fixtures.callReplay,
  fixtures.callCut,
  fixtures.callBin,
  fixtures.callRefused,
  fixtures.callPending,
  fixtures.callUncaptured,
];
const hostileCalls = [fixtures.hostileHtml, fixtures.hostileJson, fixtures.hostileForm];
const notFound = () =>
  HttpResponse.json(
    { title: "Not Found", status: 404, code: "not_found", detail: "hook not found" },
    { status: 404, headers: { "Content-Type": "application/problem+json" } },
  );

/** handlers answer as the dev user (receiver and admin) would be answered. Tests override with server.use. */
export const handlers = [
  http.get("/api/v1/me", () => HttpResponse.json(fixtures.me)),
  http.get("/api/v1/hooks", ({ request }) =>
    HttpResponse.json(
      new URL(request.url).searchParams.get("all") === "true" ? fixtures.hooksAll : fixtures.hooks,
    ),
  ),
  http.get("/api/v1/hooks/:id", ({ params }) => {
    const h = byId(
      [fixtures.hook, fixtures.idle, fixtures.busy, fixtures.hostile, fixtures.theirs],
      params.id,
    );
    return h ? HttpResponse.json(h) : notFound();
  }),
  http.get("/api/v1/hooks/:id/calls", ({ params }) => {
    if (params.id === fixtures.hook.id) return HttpResponse.json(fixtures.calls);
    if (params.id === fixtures.busy.id) return HttpResponse.json(fixtures.busyCalls);
    if (params.id === fixtures.hostile.id)
      return HttpResponse.json(
        hostileCalls.map(
          ({ id, hookId, receivedAt, kind, sender, method, path, outcome, status, duration }) => ({
            id,
            hookId,
            receivedAt,
            kind,
            sender,
            method,
            path,
            outcome,
            status,
            duration,
          }),
        ),
      );
    return HttpResponse.json([]);
  }),
  http.get("/api/v1/hooks/:id/calls/:callId", ({ params }) => {
    const c = byId([...calls, ...hostileCalls], params.callId);
    return c ? HttpResponse.json(c) : notFound();
  }),
];

export const server = setupServer(...handlers);
