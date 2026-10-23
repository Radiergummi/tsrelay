---
title: Errors
description: Every error code the relay returns in its Tsrelay-Error header and problem documents, what it means, and what to do about it.
category: reference
tags: [errors, problem details, status codes]
---

# Errors

Errors produced by the relay itself are [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) `application/problem+json` documents, and carry their code twice: in the `Tsrelay-Error` header, and as the `code` field of the body. The `type` field links to the code's entry on this page, such as `https://tsrelay.mazetti.me/errors/tunnel_unavailable`; `detail` says what exactly went wrong.

```json
{
  "type": "https://tsrelay.mazetti.me/errors/tunnel_unavailable",
  "title": "Tunnel Unavailable",
  "status": 561,
  "detail": "no tunnel is attached to this hook",
  "code": "tunnel_unavailable"
}
```

Responses from your app are never marked, including its own 5xx. Tunnel and transport failures have statuses of their own, 561 to 565, so they can't be mistaken for your app's 502, 503 or 504; clients that don't know them treat them as 500.

## Calling a hook

Senders see these when they call a hook.

### `sender_forbidden`

**403.** The caller has no sender grant: calling a hook needs the `sender` role on the relay. Ask a relay admin to grant the calling device the sender role; see [Access control](access-control.md).

### `hook_not_found`

**404.** No hook answers at this path, the hook is in a channel the caller can't use, or it was deleted or expired while a call was held for it. Check the URL; its owner can list their hooks with `tsrelay hooks list`.

### `body_stalled`

**408.** The sender paused the request body for longer than the hook's `idleTimeout`. The hook's owner can raise it with `tsrelay hooks update <hook> --idle-timeout 1m`.

### `body_too_large`

**413.** The request body is larger than the hook's `maxBodyBytes`, or, for a call from the internet, larger than the relay's `--funnel-max-body-bytes`. The hook's owner can raise the limit with `tsrelay hooks update <hook> --max-body-bytes 10485760`, up to the relay's maximum.

### `path_too_long`

**414.** The call's path, percent-encoded, is longer than 8 KiB. The relay refuses it before routing, like a server refusing an overlong request line, so it doesn't reach the inspector or the audit log. Shorten the path; send long data in the body.

### `shutting_down`

**503.** The relay is shutting down, usually for a restart, while the call waited for a tunnel; one arriving then gets it too. Calls already delivered to a tunnel finish first. `Retry-After` is 5: retry then, and the restarted relay holds the call until the tunnel is back.

### `rate_limited`

**429.** The hook's `rateLimit` was exceeded. `Retry-After` is the limiter's wait, rounded up to whole seconds. Wait, then retry; the hook's owner can raise the limit with `tsrelay hooks update <hook> --rate-limit 100/200`.

### `tunnel_unavailable`

**561 Tunnel Unavailable.** No tunnel attached to the hook within its `holdTimeout`. `Retry-After` is the `holdTimeout`, but never less than 5 s. The hook's owner can attach one with `tsrelay attach <hook> --to 3000`, or wait longer with `tsrelay hooks update <hook> --hold-timeout 1m`.

### `hold_queue_full`

**562 Tunnel Overloaded.** No tunnel is attached to the hook, and too many calls are already waiting for one (the relay's `--hook-hold-queue`). `Retry-After` is the `holdTimeout`, but never less than 5 s. The hook's owner can attach a tunnel with `tsrelay attach <hook> --to 3000`.

### `tunnel_overloaded`

**562 Tunnel Overloaded.** Too many calls are in flight on the tunnel at once (the relay's `--tunnel-concurrency`). `Retry-After` is 1; retry in a moment.

### `tunnel_lost`

**563 Tunnel Lost.** The `tsrelay` process serving the hook disconnected while delivering the call. It reconnects on its own; check that it still runs.

### `upstream_unreachable`

**564 Upstream Unreachable.** The tunnel is up, but nothing answered at its target. Start the app, or check the port or URL `tsrelay` forwards to.

### `upstream_timeout`

**565 Upstream Timeout.** The app didn't send its response headers within the hook's `responseTimeout`, or its response stalled past the `idleTimeout`. The hook's owner can raise it with `tsrelay hooks update <hook> --response-timeout 1m`.

## Call outcomes

A call's `outcome` in the [inspector](web-ui.md#inspecting-calls) and the API is `pending`, `ok`, one of the codes above, or `sender_canceled`: the sender went away before the call finished, so no response was sent and no error is recorded against the hook.

## Using the API

The [API](api.md), the web UI and the CLI see these. Where one says the relay may not speak your `tsrelay`'s API, install the relay's version of the CLI; see [Updating the CLI](getting-started.md#updating-the-cli).

### `forbidden`

**403.** Your grants don't allow this: a missing role, or a hook you can see but only its owner can act on. Ask a relay admin to grant you the role; the relay's web UI shows them the grant.

### `not_identified`

**403.** Tailscale couldn't tell the relay who is calling, so no grant applies. Check `tailscale status`, and reach the relay over Tailscale, not another network.

### `cross_site`

**403.** An API write came from another site, as the browser's `Sec-Fetch-Site` header says. API writes must come from the relay's own web UI or from a tool such as the CLI.

### `channel_forbidden`

**403.** The hook is in a channel your grants no longer match, so a tunnel can't serve it. Ask a relay admin to grant you the channel.

### `stream_limit`

**429.** The node already has 8 event streams open, the most the relay allows one node. Close a browser tab or a client that watches the relay, then retry.

### `tunnel_limit`

**403.** You already have as many tunnels attached as your `maxTunnels` grant allows. Stop one you no longer need with `tsrelay stop`, or ask a relay admin to raise the limit.

### `not_found`

**404.** The relay has no such hook or call for you. `tsrelay hooks list` shows the hooks you own.

### `invalid`

**422.** Something sent isn't valid, such as a hook setting above the relay's maximum. The problem's `errors` field names each one; correct them, then retry.

### `unsupported_field`

**422.** The request has a field this relay doesn't know, so it is newer than the relay; within one major version, this is how an older relay says so. The problem's `errors` field names each one, like `invalid` does. Ask the relay's operator to upgrade the relay, or install the CLI version the relay runs.

### `bad_request`

**400.** The relay couldn't read the request: malformed JSON, a tunnel request without its upgrade headers, or one that names no hook. This `tsrelay` may not speak the relay's API.

### `not_public`

**409.** The hook isn't public, so it has no public URL to rotate. Make it public first with `tsrelay hooks update <hook> --public`.

### `superseded`

**409.** A newer tunnel has taken the hook over, and a tunnel that reconnects after being replaced may not take it back. Stop the other tunnel, or attach again without resuming.

### `attach_rejected`

**409.** The hook's `attachPolicy` is `reject` and a tunnel is already attached. Stop that one, or set the policy to `takeover` with `tsrelay hooks update <hook> --attach-policy takeover`.

### `call_pending`

**409.** The call is still being delivered, so it can't be replayed yet. Retry once it has an outcome.

### `call_refused`

**409.** The relay refused the call by channel and never delivered it, so there is nothing to replay. Send a new test request instead.

### `not_captured`

**409.** The call can't be replayed because its request wasn't captured in full: bodies aren't captured for the hook, or the body or the query string was cut off at the capture limit. Turn capture on with `tsrelay hooks update <hook> --capture` to replay future calls.

### `misdirected_request`

**421.** The relay doesn't answer to the host name the request used. Check `--relay` and `TSRELAY_RELAY`.

### `unsupported_media_type`

**415.** API requests with a body must be `application/json`. This `tsrelay` may not speak the relay's API.

### `not_acceptable`

**406.** The resource isn't available in the representation the `Accept` header or extension asked for; the detail lists those it is available in.

### `method_not_allowed`

**405.** The resource exists, but doesn't take this method. This `tsrelay` may not speak the relay's API.

### `internal`

**500.** Something went wrong on the relay itself, such as failing to save a hook. Retry; if it keeps failing, ask the relay's operator to check its logs.
