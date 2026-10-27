import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import type { ChannelDetail, Channels, Hook } from "@/api/schemas";
import { matchChannel, validChannel } from "@/lib/channels";
import { fixtures } from "./fixtures";

const byId = (list: { id: string }[], id: unknown) => list.find((item) => item.id === id);
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

const scoped = (request: Request) => {
  const params = new URL(request.url).searchParams;
  return params.get("all") === "true" ? fixtures.hooksAll : fixtures.hooks;
};
const receive = fixtures.me.roles["receiver"] ?? [];

/** channels is GET /channels as the relay answers it for the dev user's hooks (all: every user's, counted). */
function channels(list: Hook[], all: boolean): Channels {
  const names = [...new Set(list.map((hook) => hook.channel))].sort();
  return {
    receive,
    send: [],
    channels: names.map((name) => {
      const inIt = list.filter((hook) => hook.channel === name);
      return {
        name,
        hookCount: inIt.length,
        ...(all ? {} : { hooks: inIt }),
        receive: receive.filter((pattern) => matchChannel(pattern, name)),
      };
    }),
  };
}

/** handlers answer as the dev user (receiver and admin) would be answered. Tests override with server.use. */
export const handlers = [
  http.get("/", () => HttpResponse.json(fixtures.me)),
  http.get("/hooks", ({ request }) => {
    const channel = new URL(request.url).searchParams.get("channel");
    return HttpResponse.json({
      hooks: scoped(request).filter((hook) => !channel || hook.channel === channel),
    });
  }),
  http.get("/channels", ({ request }) =>
    HttpResponse.json(
      channels(scoped(request), new URL(request.url).searchParams.get("all") === "true"),
    ),
  ),
  http.get("/channels/:name", ({ request, params }) => {
    const name = String(params.name);
    if (!validChannel(name))
      return HttpResponse.json(
        { title: "Not Found", status: 404, code: "not_found", detail: "no such channel" },
        { status: 404, headers: { "Content-Type": "application/problem+json" } },
      );
    return HttpResponse.json({
      name,
      canReceive: receive.some((pattern) => matchChannel(pattern, name)),
      canSend: false,
      hooks: scoped(request).filter((hook) => hook.channel === name),
    } satisfies ChannelDetail);
  }),
  http.get("/hooks/:id", ({ params }) => {
    const hook = byId(
      [fixtures.hook, fixtures.idle, fixtures.busy, fixtures.hostile, fixtures.theirs],
      params.id,
    );
    return hook ? HttpResponse.json(hook) : notFound();
  }),
  http.get("/hooks/:id/calls", ({ params }) => {
    if (params.id === fixtures.hook.id) return HttpResponse.json({ calls: fixtures.calls });
    if (params.id === fixtures.busy.id) return HttpResponse.json({ calls: fixtures.busyCalls });
    if (params.id === fixtures.hostile.id)
      return HttpResponse.json({
        calls: hostileCalls.map(
          ({
            id,
            hookId,
            receivedAt,
            kind,
            sender,
            method,
            path,
            outcome,
            status,
            durationMs,
          }) => ({
            id,
            hookId,
            receivedAt,
            kind,
            sender,
            method,
            path,
            outcome,
            status,
            durationMs,
          }),
        ),
      });
    return HttpResponse.json({ calls: [] });
  }),
  http.get("/hooks/:id/calls/:callId", ({ params }) => {
    const call = byId([...calls, ...hostileCalls], params.callId);
    return call ? HttpResponse.json(call) : notFound();
  }),
];

export const server = setupServer(...handlers);
