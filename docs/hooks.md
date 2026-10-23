---
title: Hooks
description: Create, configure and manage the hooks that deliver callbacks to your machine, including public hooks and channels.
category: guide
tags: [hooks, channels, public, cli]
---

# Hooks

A hook is a URL on the relay that delivers to whichever of your devices has a tunnel attached to it. `tsrelay http` creates one for you, or reuses the one it created before: running `tsrelay http 8080` again in the same directory prints the same URL, so you can keep it in your `.env`.

Useful flags:

- `-n`/`--name payments/v1` picks the path yourself.
- `-p`/`--persistent` keeps the hook after the command exits. Without it, the hook is *ephemeral*: the relay deletes it once no tunnel has been attached for a while. While no tunnel is attached to a persistent hook, callers get `561` with `Retry-After`.
- `--relay host` points at a relay that isn't at `tsrelay.<your tailnet>`.

`tsrelay http` also accepts `-- cmd args...` to start a local process with `TSRELAY_URL` set in its environment, and stop it (SIGTERM, then a kill after 5 s) when the tunnel ends:

```bash
tsrelay http 8080 -- npm run dev
```

The command keeps the `TSRELAY_URL` it was started with, even if the hook's path changes while it runs.

To keep a tunnel running without holding on to a terminal, see [Background tunnels](background-tunnels.md).

## Public hooks

A hook can also be made reachable from the internet, for third-party webhooks (such as Stripe, GitHub or Twilio) that can't reach your tailnet. It's opt-in twice over: the operator runs the relay with `--funnel` and grants you `"public": true` (see [Serving public hooks](running-a-relay.md#serving-public-hooks)), and you mark the hook `public`:

```bash
tsrelay http 3000 --public      # make this hook's URL reachable from the internet
```

Without the `public` grant, turning a hook public fails. The relay prints both URLs; the public one is a secret, so treat it like one, and rotate it with `tsrelay hooks rotate-url <hook>` if it leaks. An ephemeral `tsrelay http --public` hook gets a new public URL once it's reaped (after the relay's `--ephemeral-grace`, 10 minutes by default, without a tunnel), so use `-p`/`--persistent` (or `tsrelay hooks create --public`) for a stable URL to give to a provider.

> [!WARNING]
> The relay doesn't verify provider signatures. Your app has to, as it would without tsrelay; see the [security policy](https://github.com/Radiergummi/tsrelay/blob/main/SECURITY.md).

## Channels

Every hook belongs to a channel, `default` unless you pick another with `--channel`. Your grant says which channels you may create hooks in, and senders' grants say which channels they may call. So a channel decides who can reach your hook. Ask your operator which channels exist on your tailnet.

The web UI's Channels page lists your patterns and the channels your hooks are in, and each channel's page shows its hooks and whether you may receive and send on it. Channel names in the hook list link there, and the hook list can be filtered by channel. The same data is in the API: `GET /channels`, `GET /channels/{name}`, and `GET /hooks?channel=prod.payments` for the hooks in one channel. Who else may send on a channel isn't something the relay knows; that's in the tailnet policy.

## Managing hooks

```bash
tsrelay hooks list                                 # your hooks (alias: ls)
tsrelay hooks show <hook>                          # one hook and its effective settings
tsrelay hooks create [<path>] [flags]              # create a persistent hook
tsrelay hooks update <hook> [flags]                # change one (only the given flags)
tsrelay hooks delete <hook>...                     # delete one or more (alias: rm)
tsrelay hooks rotate-url <hook>                    # give a public hook a new public URL
tsrelay url [<port|url>]                           # the URL tsrelay http would use, without contacting the relay
tsrelay attach <hook>... -t <port|url>             # serve existing hooks from one local target
```

`<hook>` is a hook's ID or path. `-k`/`--insecure` (on `http` and `attach`) skips TLS certificate verification for an `https://` target.

If a served hook's path is changed (`tsrelay hooks update`, the web UI or the API), `http` and `attach` print its new URL on stdout, and `tsrelay http` remembers it.

## Hook settings

Every hook has delivery settings. Your relay sets a default and, for most of them, a maximum; a hook may only choose values within the maximum (see [Hook defaults and limits](configuration.md#hook-defaults-and-limits)).

| Setting | Flag | |
|---|---|---|
| `attachPolicy` | `--attach-policy` | `takeover` replaces an attached tunnel with a new one; `reject` refuses the new one with `409 attach_rejected` |
| `hostHeader` | `--host-header` | `preserve` passes the relay's host to your app; `rewrite` sets `Host` to your local target's host |
| `holdTimeout` | `--hold-timeout` | How long a call waits for a tunnel to attach; `0s` disables holding |
| `responseTimeout` | `--response-timeout` | How long to wait for your app's response headers |
| `idleTimeout` | `--idle-timeout` | Longest gap in body traffic, either direction, before the call fails |
| `maxBodyBytes` | `--max-body-bytes` | Request body size limit; `0` is unlimited |
| `rateLimit` | `--rate-limit` | Calls per second and burst, as `perSecond/burst` |
| `capture` | `--capture` | Keep request and response bodies for the inspector, which relays don't do by default; metadata and headers are always recorded |

Set them when creating or updating a hook:

```bash
tsrelay hooks create payments/v1 --rate-limit 10/20 --host-header rewrite
tsrelay hooks update payments/v1 --hold-timeout 0s
```

`tsrelay http` takes the same flags; they apply when it creates the hook, not to a hook it reuses. `tsrelay hooks show <hook>` lists the effective values, defaults included.

A hook's `public` flag is also a setting (`--public`), but it has no relay-wide default or maximum: it's governed by the `public` grant and `--funnel` instead (see [Public hooks](#public-hooks)). Unlike the other settings, `tsrelay http --public` also changes a hook it reuses.
