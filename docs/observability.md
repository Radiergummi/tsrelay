---
title: Observability
description: Health checks, Prometheus metrics, OpenTelemetry traces, the audit log and profiling for a running relay.
category: guide
tags: [operators, metrics, prometheus, opentelemetry, tracing, pprof]
---

# Observability

## Audit log

The audit log is part of the relay's log on stderr, one JSON object per line (or `key=value` text with `--log-format text`). An audit record's `msg` is its event name, and its `level` is `INFO`. It is written whatever `--log-level` says: `--log-level warn` quiets the relay's own messages, not the audit log. Records never contain bodies, query strings, or header values.

`actor` is the node that caused the event: `{"node", "login", "tags"}`, with an empty `login` for a tagged machine. `hook` is `{"id", "owner", "path", "channel"}`, where `owner` is the owner's login.

| Event | Fields | Logged when |
|---|---|---|
| `hook.create` | `actor`, `hook`, `ephemeral` | A hook was created |
| `hook.update` | `actor`, `hook`, `changed` (the names of the changed settings) | A hook was changed |
| `hook.delete` | `actor`, `hook` | Its owner or an admin deleted a hook |
| `hook.expire` | `hook` | The relay removed an ephemeral hook without a tunnel, or a hook past its expiry |
| `tunnel.attach` | `actor`, `tunnel`, `hooks` | A tunnel attached, serving the hook IDs in `hooks` |
| `tunnel.detach` | `actor`, `tunnel` | A tunnel ended |
| `tunnel.replaced` | `tunnel`, `by`, `hooks` | Tunnel `by` took the hooks in `hooks` over from `tunnel` |
| `tunnel.revoked` | `actor`, `tunnel`, `hooks`, `reason` | The relay detached hooks from a tunnel whose node may no longer serve them |
| `call` | `callID`, `kind`, `hookID`, `actor` (the sender), `method`, `path`, `status`, `error`, `bytesIn`, `bytesOut`, `waited`, `duration`, `ttfb` (when the app's response headers arrived), `clientIP` (calls from the internet only) | A call ended. `path` is below the hook's URL, without the query string; `error` is the `Tsrelay-Error` code, if the relay sent one |
| `authz.deny` | `action`, `reason`, and `actor` and `hook` where known | The relay refused a call, an attach or a hook creation for lack of a grant, or over a limit |

Durations are nanoseconds in JSON. A grant value the relay skips or trims is logged as `cap.parse_error`, with `node` and `error`, at `WARN` and at most once per node and error.

## Metrics

With `--ops-listen`, the relay serves `/healthz` (process alive), `/readyz` (tailnet joined and the hook store loaded) and `/metrics` (Prometheus text format: the counters and gauges `tsrelay_calls_total`, `tsrelay_call_bytes_total`, `tsrelay_tunnels`, `tsrelay_hooks`, `tsrelay_held_calls`, `tsrelay_authz_denials_total`; the latency histograms `tsrelay_call_duration_seconds`, `tsrelay_call_ttfb_seconds`, `tsrelay_call_hold_seconds`, `tsrelay_api_request_duration_seconds`; and Go runtime metrics). `/readyz` and `/metrics` answer `503` until the relay has joined the tailnet.

The ops listener has no authentication. It listens on the host's network, not on the relay's tailnet node, so it is never reachable through the relay's tailnet address. On a host that runs `tailscaled` as well, though, `--ops-listen=:9090` also listens on that host's own tailnet address: bind it to a specific address there, such as `--ops-listen=127.0.0.1:9090`. [Running a relay](running-a-relay.md#alerts) suggests alerts.

## Tracing

With `--otel`, the relay exports traces and metrics over OTLP/HTTP. Everything else is configured with the standard OpenTelemetry variables: `OTEL_EXPORTER_OTLP_ENDPOINT` (default `http://localhost:4318`), `OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_TRACES_SAMPLER`, `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES`. An unreachable collector never stops the relay; it logs a warning at most once a minute. Spans and metrics never contain bodies, query strings, or header values.

Each call is a `tsrelay.call` span with children `tsrelay.authorize`, `tsrelay.hold` (only when the call waited for a tunnel) and `tsrelay.forward` (until the app's response headers). With `--otel-propagation` (the default), the relay joins the sender's trace and replaces `traceparent` on the way to the app, so the app's own spans nest under `tsrelay.forward`. Without `--otel`, trace headers pass through untouched.

## Profiling

With `--ops-pprof`, the ops listener also serves Go's profiler under `/debug/pprof/`, as unauthenticated as the rest of it. On Kubernetes, enable it temporarily and reach it through a port-forward:

```bash
kubectl port-forward statefulset/tsrelay 9090:9090
go tool pprof http://localhost:9090/debug/pprof/heap
```
