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

### `rate_limited`

**429.** The hook's `rateLimit` was exceeded. `Retry-After` is the limiter's wait, rounded up to whole seconds. Wait, then retry; the hook's owner can raise the limit with `tsrelay hooks update <hook> --rate-limit 100/200`.

### `tunnel_unavailable`

**561 Tunnel Unavailable.** No tunnel attached to the hook within its `holdTimeout`, or too many calls were already waiting for one. `Retry-After` is the `holdTimeout`, but never less than 5 s. The hook's owner can attach one with `tsrelay attach <hook> --to 3000`, or wait longer with `tsrelay hooks update <hook> --hold-timeout 1m`.

### `tunnel_overloaded`

**562 Tunnel Overloaded.** Too many calls are in flight on the tunnel at once (the relay's `--tunnel-concurrency`). `Retry-After` is 1; retry in a moment.

### `tunnel_lost`

**563 Tunnel Lost.** The `tsrelay` process serving the hook disconnected while delivering the call. It reconnects on its own; check that it still runs.

### `upstream_unreachable`

**564 Upstream Unreachable.** The tunnel is up, but nothing answered at its target. Start the app, or check the port or URL `tsrelay` forwards to.

### `upstream_timeout`

**565 Upstream Timeout.** The app didn't send its response headers within the hook's `responseTimeout`, or its response stalled past the `idleTimeout`. The hook's owner can raise it with `tsrelay hooks update <hook> --response-timeout 1m`.

## Using the API

The [API](api.md), the web UI and the CLI see these. Where one says the relay may not speak your `tsrelay`'s API, install the relay's version of the CLI; see [Updating the CLI](getting-started.md#updating-the-cli).

### `forbidden`

**403.** Your grants don't allow this: a missing role, a channel your patterns don't match, more tunnels than your limit allows, a cross-site request to the API, or a device Tailscale couldn't identify. Ask a relay admin to grant you the role or channel; the relay's web UI shows them the grant.

### `not_found`

**404.** The relay has no such hook or call for you. `tsrelay hooks list` shows the hooks you own.

### `invalid`

**422.** Something sent isn't valid, such as a hook setting above the relay's maximum. The problem's `errors` field names each one; correct them, then retry.

### `unsupported_field`

**422.** The request has a field this relay doesn't know, so it is newer than the relay; within one major version, this is how an older relay says so. The problem's `errors` field names each one, like `invalid` does. Ask the relay's operator to upgrade the relay, or install the CLI version the relay runs.

### `bad_request`

**400.** The relay couldn't read the request: malformed JSON, a tunnel request without its upgrade headers, or one that names no hook. This `tsrelay` may not speak the relay's API.

### `conflict`

**409.** Something is in the way: a call that is still being delivered, or whose body or query string wasn't captured in full, can't be replayed; a hook that isn't public has no public URL to rotate; and a tunnel is refused when the hook's `attachPolicy` is `reject` and another is attached, or when a newer one has taken it over. Resolve what the relay named, then retry.

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
