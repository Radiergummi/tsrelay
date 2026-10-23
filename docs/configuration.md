---
title: Configuration
description: The flags and environment variables tsrelay serve takes, and the relay-wide defaults and limits for hook settings.
category: reference
tags: [operators, configuration, flags, environment]
---

# Configuration

Every `serve` flag can also be set as an environment variable: `--ephemeral-grace` becomes `TSRELAY_EPHEMERAL_GRACE`. Run `tsrelay serve --help` for the full list. Client flags have a variable too unless they act (such as `--public`, `--yes` or `--all`) or name a target; [the CLI page](cli.md#environment) lists which.

Flag names follow one scheme, so a prefix tells you what a flag governs:

| Prefix | Governs |
|---|---|
| `hook-<setting>-default`, `hook-<setting>-max` | A [hook setting](#hook-defaults-and-limits)'s default, and the ceiling a hook may set it to |
| `user-*` | Per-user limits and naming; grants can raise the limits |
| `tunnel-*` | Tunnels |
| `capture-*` | What the inspector keeps |
| `funnel-*` | Public hooks served through Tailscale Funnel |
| `ops-*` | The operations listener |
| `otel-*` | OpenTelemetry |

| Flag | Default | |
|---|---|---|
| `--hostname` | `tsrelay` | Tailnet machine name |
| `--state-dir` | `/var/lib/tsrelay` in the container, `$XDG_STATE_HOME/tsrelay` for the binary | Directory for tailnet state and hooks |
| `--tls` | `true` | HTTPS on 443; `false` serves HTTP on 80 |
| `--advertise-tags` | `tag:tsrelay` | Tags the relay node advertises |
| `--cap-name` | `tsrelay.mazetti.me/cap/relay` | Capability that carries grants |
| `--user-slug` | `local-part` | `moritz@example.com` becomes `~moritz`; `login` uses the full login |
| `--user-slug-override` | | `alice@b.com=alice-b`, for collisions |
| `--user-max-hooks` / `--user-max-tunnels` | `10` / `5` | Per user; grants can raise them with `maxHooks` / `maxTunnels` |
| `--user-max-hook-ttl` | unlimited (`0`) | Longest a persistent hook may live. A grant's `maxHookTtl` only raises a limit set here; while this is unlimited, it has no effect |
| `--ephemeral-grace` | `10m` | How long an ephemeral hook survives without its tunnel |
| `--tunnel-concurrency` | `32` | Concurrent calls per tunnel |
| `--revalidate-interval` | `60s` | How often attached tunnels and open event streams are re-authorized |
| `--shutdown-timeout` | `20s` | How long calls already delivered get to finish when the relay stops. Keep it below the pod's termination grace period; see [restarts](running-a-relay.md#restarts) |
| `--hook-hold-queue` | `100` | Calls per hook that may wait for a tunnel to attach at once |
| `--capture-calls` | `100` | Calls per hook the inspector keeps in memory |
| `--capture-limit-bytes` | `65536` | Bytes the inspector keeps of each body, of each call's headers, and of its query string |
| `--capture-total-bytes` | `134217728` | Bytes the inspector keeps of all calls together (128 MiB); past it, the oldest calls of any hook go first. See [sizing](running-a-relay.md#sizing) |
| `--capture-bodies` | `true` | `false` keeps no bodies for any hook |
| `--redact-headers` | none | Headers whose values are never kept, in addition to `Authorization`, `Proxy-Authorization`, `Cookie`, `Set-Cookie` and `X-Api-Key`, which are always redacted; names match without regard to case |
| `--funnel` | `false` | Serve hooks their owners made public on the internet, through Tailscale Funnel; requires `--tls` |
| `--funnel-port` | `443` | The Funnel port for public hooks: `443`, `8443` or `10000` |
| `--funnel-max-body-bytes` | `26214400` (25 MiB) | Largest request body of a call from the internet |
| `--ops-listen` | off | Host address (for example `:9090`) for `/healthz`, `/readyz` and `/metrics`; never on the tailnet |
| `--ops-pprof` | `false` | Serve Go's profiler under `/debug/pprof/` on the ops listener |
| `--otel` | `false` | Export traces and metrics over OTLP/HTTP; see [Tracing](observability.md#tracing) |
| `--otel-propagation` | `true` | With `--otel`: continue a sender's `traceparent` and pass the relay's own to the app, so one trace spans sender, relay and app; `false` forwards trace headers untouched |
| `--log-format` | `json` | `json` or `text` |
| `--log-level` | `info` | `debug`, `info`, `warn` or `error` |
| `--verbose-tailscale` | `false` | Include tsnet's own logs |
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

For example `--hook-rate-limit-default=100/200`, or `--hook-max-body-bytes-default=1048576 --hook-max-body-bytes-max=10485760` (the default body size is unlimited, which a maximum wouldn't allow, so set both). A hook may only set a value within the maximum. A maximum of `0` means none. Zero isn't "no limit" everywhere, though; [compatibility](compatibility.md#what-zero-means) lists what it means for each limit.
