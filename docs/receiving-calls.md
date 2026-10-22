---
title: Receiving calls
description: What your app receives from the relay, and the errors senders get when a call can't be delivered.
category: guide
tags: [headers, signatures, errors]
---

# Receiving calls

## What your app receives

Bodies arrive exactly as sent, byte for byte, so signatures from Stripe, GitHub, Svix, or [Standard Webhooks](https://www.standardwebhooks.com/) verify normally. The relay adds these headers, after removing any copies the sender sent:

| Header | Value |
|---|---|
| `Tsrelay-Sender-Node` | The calling machine's name |
| `Tsrelay-Sender-Tags` | Its tags, comma-separated |
| `Tsrelay-Sender-Login` | Its user, if it isn't a tagged machine |
| `Tsrelay-Sender-Public` | `true` for a call from the internet to a [public hook](hooks.md#public-hooks); such a call has no `Tsrelay-Sender-Node`, `-Tags` or `-Login` |
| `Tsrelay-Hook` | `user/path` of the hook |
| `Tsrelay-Call-Id` | Also returned to the caller, for matching logs |
| `X-Forwarded-For`, `-Host`, `-Proto`, `-Prefix` | The original request, for URL-signing schemes |

## What senders get

Your app's response goes back to the sender as it is. Errors produced by the relay itself are `application/problem+json` responses and carry a `Tsrelay-Error` header. Responses from your app are never marked, including its own 5xx. Tunnel and transport failures have statuses of their own, 561 to 565, so they can't be mistaken for your app's 502, 503 or 504; clients that don't know them treat them as 500. Senders see these:

| `Tsrelay-Error` | Status | Meaning |
|---|---|---|
| [`sender_forbidden`](errors.md#sender_forbidden) | 403 | The caller has no sender grant |
| [`hook_not_found`](errors.md#hook_not_found) | 404 | No such hook, not in the caller's channels, or it was deleted/expired while a call was held for it |
| [`body_stalled`](errors.md#body_stalled) | 408 | The sender's request body stalled past `idleTimeout` |
| [`body_too_large`](errors.md#body_too_large) | 413 | Over the hook's `maxBodyBytes`, or for a call from the internet, over the relay's `--funnel-max-body-bytes` |
| [`rate_limited`](errors.md#rate_limited) | 429 | Over the hook's `rateLimit`; `Retry-After` is the limiter's wait, rounded up to whole seconds |
| [`tunnel_unavailable`](errors.md#tunnel_unavailable) | 561 | No tunnel attached within `holdTimeout`, or too many calls are already waiting; `Retry-After` is `holdTimeout`, but never less than 5 s |
| [`tunnel_overloaded`](errors.md#tunnel_overloaded) | 562 | Too many concurrent calls on the tunnel; `Retry-After: 1` |
| [`tunnel_lost`](errors.md#tunnel_lost) | 563 | The tunnel dropped mid-call |
| [`upstream_unreachable`](errors.md#upstream_unreachable) | 564 | The tunnel is up but your app isn't listening |
| [`upstream_timeout`](errors.md#upstream_timeout) | 565 | Your app didn't answer within the hook's `responseTimeout` |

Every response a sender gets carries `Tsrelay-Call-Id`, which names the call in the [inspector](web-ui.md#inspecting-calls).
