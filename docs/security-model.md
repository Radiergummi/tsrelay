---
title: Security model and limitations
description: What the relay guarantees from tailnet identity and grants, whom you have to trust, what it keeps, and what it contacts outside your tailnet.
category: guide
tags: [security, operators, trust, limitations]
---

# Security model and limitations

This page says what tsrelay protects, whom you have to trust, and where its limits are. To report a vulnerability, follow the [security policy](https://github.com/Radiergummi/tsrelay/blob/main/SECURITY.md).

## What identity and grants guarantee

- **The relay knows every tailnet caller.** It asks Tailscale who is behind each connection's tailnet address, so a caller can't claim to be someone else. It removes every incoming `Tsrelay-*` header, so the `Tsrelay-Sender-*` headers your app receives are the relay's, not the sender's.
- **Grants decide everything.** A sender reaches only hooks in channels its grant matches; for any other hook it gets the same `404` as for one that doesn't exist. Only a hook's owner can attach a tunnel to it, edit it, or see its calls' queries, headers and bodies. [Access control](access-control.md) has the rules.
- **Revoking a grant takes effect** on the caller's next request, and for tunnels that are already attached within one `--revalidate-interval` (60 s).
- **Developer machines accept no connections.** The CLI dials out to the relay, and calls come back down that connection. Production needs no route to a laptop.

## Public hooks are bearer URLs

With `--funnel`, a hook its owner made public accepts calls from anyone on the internet who has its URL. The relay doesn't authenticate them and doesn't verify provider signatures: your app has to, as it would without tsrelay. A call from the internet carries `Tsrelay-Sender-Public: true` and no tailnet identity. If a public URL leaks, rotate it with `tsrelay hooks rotate-url <hook>`; the old one stops working at once.

## Whom you trust

- **The relay sees everything in the clear.** It ends the senders' TLS connections and forwards each call down a tunnel, so it can read every body, header and query string, whether or not a hook captures them. Whoever runs the relay process, can read its memory or its state directory, or administers the cluster or host it runs on, can read and change all traffic through it, and can act as the relay's tailnet device. The `admin` role doesn't change that: it only limits what the web UI and the API show.
- **A hook exposes all of its target.** Every sender in the hook's channel can call any path below the hook's URL, with any method, and the call reaches the local app as it is. Don't point a hook at an app whose other routes you wouldn't let those senders call, and have your app check what it receives.
- **Your app's responses can't use the relay's origin.** Responses to calls carry `Content-Security-Policy: sandbox …`, so a page your app serves through a hook runs in an opaque origin and can't call the relay's API as whoever opened it.

## What the relay keeps

- **The hook store**, `hooks.json` in the state directory, holds hooks, their settings and public URLs, and which user each `~slug` belongs to. Next to it, `tsnet/` holds the relay's node key and TLS certificates. Protect the state directory like a credential.
- **The inspector** keeps each hook's most recent calls in memory only, never on disk, and loses them when the relay restarts. Of each call it keeps the path, the query string and the headers, minus `Authorization`, `Proxy-Authorization`, `Cookie`, `Set-Cookie`, `X-Api-Key` and any headers named with `--redact-headers`. It keeps bodies only for hooks that turn capture on, and never with `--capture-bodies=false`. The query, the headers and each body are cut at `--capture-limit-bytes`, and all calls together at `--capture-total-bytes` (128 MiB), past which the oldest go first, so no sender can make the inspector outgrow the relay's memory. A call whose path is longer than 8 KiB is refused with `414 path_too_long`, not cut short, before it reaches the inspector or the audit log.
- **The audit log** names hooks, senders, methods, paths and statuses, but never bodies, query strings or header values. [Observability](observability.md#audit-log) lists its fields.

## Availability

- **One replica, no high availability.** While the relay is down, calls fail.
- **A restart interrupts delivery.** Calls held for a tunnel are answered `503 shutting_down`, calls already delivered get `--shutdown-timeout` (20 s) to finish, and calls that arrive while the relay is down fail to connect. The inspector's history is gone. Hooks survive, and the CLI reconnects on its own; [Restarts](running-a-relay.md#restarts) has the details.
- **tsrelay never retries a call.** A call that fails, whether at the relay or because a tunnel dropped mid-call, is answered with an error, and the sender has to retry. While no tunnel is attached, a call waits for one up to the hook's `holdTimeout`, and that is all.

## What it contacts outside your tailnet

- **The relay** talks to Tailscale's coordination server and relays, as every Tailscale device does, and with `--tls` to the certificate authority it gets its HTTPS certificate from, as `tailscale cert` does. Like other Tailscale clients, its embedded Tailscale sends its client logs to Tailscale's log server; set `TS_NO_LOGS_NO_SUPPORT=true` to stop that. It asks GitHub's API for the latest tsrelay release on startup and every 6 hours, unless you pass `--update-check=false`. With `--otel`, it sends traces and metrics to the collector you configure.
- **The CLI** talks to your machine's Tailscale and to the relay. `install.sh` and `install.ps1` download the release from GitHub. `tsrelay update` asks the relay only for its version, which has to be a release version, and downloads that release's binary and checksum from GitHub itself, so a relay can't hand you a binary of its own.

## The ops listener

`--ops-listen` serves `/healthz`, `/readyz` and `/metrics`, and with `--ops-pprof` Go's profiler, without authentication. It isn't on the relay's tailnet node, but it listens on the host's network: don't expose it beyond your monitoring, and on a host that runs `tailscaled`, bind it to a specific address rather than all of them.
