---
title: Observability
description: Health checks, Prometheus metrics, OpenTelemetry traces, the audit log and profiling for a running relay.
category: guide
tags: [operators, metrics, prometheus, opentelemetry, tracing, pprof]
---

# Observability

## Audit log

The audit log is JSON on stderr. It never contains bodies, query strings, or header values.

## Metrics

With `--ops-listen=:9090`, the relay serves `/healthz` (process alive), `/readyz` (tailnet joined and the hook store loaded) and `/metrics` (Prometheus text format: the counters and gauges `tsrelay_calls_total`, `tsrelay_call_bytes_total`, `tsrelay_tunnels`, `tsrelay_hooks`, `tsrelay_held_calls`, `tsrelay_authz_denials_total`; the latency histograms `tsrelay_call_duration_seconds`, `tsrelay_call_ttfb_seconds`, `tsrelay_call_hold_seconds`, `tsrelay_api_request_duration_seconds`; and Go runtime metrics). The ops listener is never on the tailnet interface.

## Tracing

With `--otel`, the relay exports traces and metrics over OTLP/HTTP. Everything else is configured with the standard OpenTelemetry variables: `OTEL_EXPORTER_OTLP_ENDPOINT` (default `http://localhost:4318`), `OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_TRACES_SAMPLER`, `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES`. An unreachable collector never stops the relay; it logs a warning at most once a minute. Spans and metrics never contain bodies, query strings, or header values.

Each call is a `tsrelay.call` span with children `tsrelay.authorize`, `tsrelay.hold` (only when the call waited for a tunnel) and `tsrelay.forward` (until the app's response headers). With `--trace-propagation` (the default), the relay joins the sender's trace and replaces `traceparent` on the way to the app, so the app's own spans nest under `tsrelay.forward`. Without `--otel`, trace headers pass through untouched.

## Profiling

With `--ops-pprof`, the ops listener also serves Go's profiler under `/debug/pprof/`. On Kubernetes, enable it temporarily and reach it through a port-forward:

```bash
kubectl port-forward statefulset/tsrelay 9090:9090
go tool pprof http://localhost:9090/debug/pprof/heap
```
