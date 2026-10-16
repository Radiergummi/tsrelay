import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import type { ChannelDetail, Channels, Hook } from "@/api/schemas";
import { matchChannel } from "@/lib/channels";
import { fixtures } from "./fixtures";

const byId = (list: { id: string }[], id: unknown) => list.find((x) => x.id === id);
const calls = [
  fixtures.callJson,
  fixtures.callReplay,
  fixtures.callCut,
  fixtures.callBin,
  fixtures.callRefused,
  fixtures.callPending,
];
const hostileCalls = [fixtures.hostileHtml, fixtures.hostileJson, fixtures.hostileForm];
const notFound = () =>
  HttpResponse.json(
    { title: "Not Found", status: 404, code: "not_found", detail: "hook not found" },
    { status: 404, headers: { "Content-Type": "application/problem+json" } },
  );

const scoped = (request: Request) => {
  const q = new URL(request.url).searchParams;
  return q.get("all") === "true" ? fixtures.hooksAll : fixtures.hooks;
};
const receive = fixtures.me.roles["receiver"] ?? [];

/** channels is GET /channels as the relay answers it for the dev user's hooks (all: every user's, counted). */
function channels(list: Hook[], all: boolean): Channels {
  const names = [...new Set(list.map((h) => h.channel))].sort();
  return {
    receive,
    send: [],
    channels: names.map((name) => {
      const inIt = list.filter((h) => h.channel === name);
      return {
        name,
        hookCount: inIt.length,
        ...(all ? {} : { hooks: inIt }),
        receive: receive.filter((p) => matchChannel(p, name)),
      };
    }),
  };
}

/** handlers answer as the dev user (receiver and admin) would be answered. Tests override with server.use. */
export const handlers = [
  http.get("/", () => HttpResponse.json(fixtures.me)),
  http.get("/hooks", ({ request }) => {
    const channel = new URL(request.url).searchParams.get("channel");
    return HttpResponse.json(scoped(request).filter((h) => !channel || h.channel === channel));
  }),
  http.get("/channels", ({ request }) =>
    HttpResponse.json(
      channels(scoped(request), new URL(request.url).searchParams.get("all") === "true"),
    ),
  ),
  http.get("/channels/:name", ({ request, params }) => {
    const name = String(params.name);
    if (!/^[a-z0-9-]+(\.[a-z0-9-]+)*$/.test(name))
      return HttpResponse.json(
        { title: "Not Found", status: 404, code: "not_found", detail: "no such channel" },
        { status: 404, headers: { "Content-Type": "application/problem+json" } },
      );
    return HttpResponse.json({
      name,
      canReceive: receive.some((p) => matchChannel(p, name)),
      canSend: false,
      hooks: scoped(request).filter((h) => h.channel === name),
    } satisfies ChannelDetail);
  }),
  http.get("/hooks/:id", ({ params }) => {
    const h = byId(
      [fixtures.hook, fixtures.idle, fixtures.busy, fixtures.hostile, fixtures.theirs],
      params.id,
    );
    return h ? HttpResponse.json(h) : notFound();
  }),
  http.get("/hooks/:id/calls", ({ params }) => {
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
  http.get("/hooks/:id/calls/:callId", ({ params }) => {
    const c = byId([...calls, ...hostileCalls], params.callId);
    return c ? HttpResponse.json(c) : notFound();
  }),
];

export const server = setupServer(...handlers);
