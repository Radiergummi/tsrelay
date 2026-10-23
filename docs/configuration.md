---
title: Configuration
description: The flags and environment variables tsrelay serve takes, and the relay-wide defaults and limits for hook settings.
category: reference
tags: [operators, configuration, flags, environment]
---

# Configuration

Every flag can also be set as an environment variable: `--ephemeral-grace` becomes `TSRELAY_EPHEMERAL_GRACE`. Run `tsrelay serve --help` for the full list. The most relevant flags:

| Flag | Default | |
|---|---|---|
| `--hostname` | `tsrelay` | Tailnet machine name |
| `--tls` | `true` | HTTPS on 443; `false` serves HTTP on 80 |
| `--funnel` | `false` | Serve hooks their owners made public on the internet, through Tailscale Funnel; requires `--tls` |
| `--funnel-port` | `443` | The Funnel port for public hooks: `443`, `8443` or `10000` |
| `--funnel-max-body-bytes` | `26214400` (25 MiB) | Largest request body of a call from the internet |
| `--advertise-tags` | `tag:tsrelay` | |
| `--cap-name` | `mazetti.me/cap/relay` | Capability that carries grants |
| `--user-slug` | `local-part` | `moritz@example.com` becomes `~moritz`; `login` uses the full login |
| `--user-slug-override` | | `alice@b.com=alice-b`, for collisions |
| `--max-hooks-default` / `--max-tunnels-default` | `10` / `5` | Per user; grants can raise them with `maxHooks` / `maxTunnels` |
| `--max-hook-ttl-default` | unlimited | Longest a persistent hook may live; grants can raise it with `maxHookTtl` |
| `--ephemeral-grace` | `10m` | How long an ephemeral hook survives without its tunnel |
| `--tunnel-concurrency` | `32` | Concurrent calls per tunnel |
| `--hold-queue` | `100` | Calls per hook that may wait for a tunnel to attach at once |
| `--revalidate-interval` | `60s` | How often an attached tunnel's grant is re-checked |
| `--capture-calls` | `100` | Calls per hook the inspector keeps in memory |
| `--capture-limit` | `65536` | Bytes the inspector keeps of each body, of each call's headers, and of its query string |
| `--capture-bodies` | `true` | `false` keeps no bodies for any hook |
| `--redact-headers` | `Authorization,Proxy-Authorization,Cookie,Set-Cookie,X-Api-Key` | Headers whose values are never kept; an empty value keeps these defaults, so redaction can't be turned off |
| `--ops-listen` | off | Host address (for example `:9090`) for `/healthz`, `/readyz` and `/metrics`; never on the tailnet |
| `--ops-pprof` | `false` | Serve Go's profiler under `/debug/pprof/` on the ops listener |
| `--otel` | `false` | Export traces and metrics over OTLP/HTTP; see [Tracing](observability.md#tracing) |
| `--trace-propagation` | `true` | With `--otel`: continue a sender's `traceparent` and pass the relay's own to the app, so one trace spans sender, relay and app; `false` forwards trace headers untouched |
| `--update-check` | `true` | Ask GitHub for the latest tsrelay release on startup and every 6 hours, and tell admins when it's newer than the relay; `false` for air-gapped relays |

## Hook defaults and limits

Each [hook setting](hooks.md#hook-settings) has a relay-wide default, `--hook-<setting>-default`, and the numeric and duration settings also a maximum, `--hook-<setting>-max`:

| Setting | Default | Maximum |
|---|---|---|
| `attachPolicy` | `takeover` | |
| `hostHeader` | `preserve` | |
| `holdTimeout` | `30s` | `5m` |
| `responseTimeout` | `2m` | `30m` |
| `idleTimeout` | `60s` | `30m` |
| `maxBodyBytes` | unlimited | none |
| `rateLimit` | `100/200` | none |
| `capture` | `false` | |

For example `--hook-rate-limit-default=100/200`, or `--hook-max-body-bytes-default=1048576 --hook-max-body-bytes-max=10485760` (the default body size is unlimited, which a maximum wouldn't allow, so set both). A hook may only set a value within the maximum.
