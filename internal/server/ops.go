package server

import (
	"cmp"
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Ops serves the ops listener: /healthz, /readyz and /metrics. It exists
// before the relay is up, so liveness probes pass while tsnet joins the
// tailnet; /readyz and /metrics answer 503 until Ready is called.
type Ops struct{ srv atomic.Pointer[Server] }

// Ready marks the relay as up: tsnet joined, the store loaded, s serving.
func (o *Ops) Ready(s *Server) { o.srv.Store(s) }

func (o *Ops) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s := o.srv.Load()
	switch p := r.URL.Path; {
	case p == "/healthz":
		io.WriteString(w, "ok\n")
	case p != "/readyz" && p != "/metrics":
		http.NotFound(w, r)
	case s == nil:
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	case p == "/metrics":
		s.tel.Metrics.ServeHTTP(w, r)
	default:
		io.WriteString(w, "ok\n")
	}
}

// newMetrics builds the relay's metrics (DESIGN.md §13). Counters and
// histograms derive from audit events through the returned observer, so the
// request path has no instrumentation of its own; gauges are read when the
// metrics are collected.
func (s *Server) newMetrics(mp metric.MeterProvider) func(event string, attrs []slog.Attr) {
	m := mp.Meter(telemetry.ScopeName)
	calls := must(m.Int64Counter("tsrelay_calls_total", metric.WithDescription("Callback calls, by outcome: ok or the relay's error code.")))
	callBytes := must(m.Int64Counter("tsrelay_call_bytes_total", metric.WithDescription("Callback body bytes, in from senders and out to them.")))
	denials := must(m.Int64Counter("tsrelay_authz_denials_total", metric.WithDescription("Requests refused by authorization, by action.")))
	buckets := metric.WithExplicitBucketBoundaries(telemetry.Buckets...)
	duration := must(m.Float64Histogram("tsrelay_call_duration_seconds", buckets,
		metric.WithDescription("Callback calls from arrival to the last byte back, by outcome and kind.")))
	ttfb := must(m.Float64Histogram("tsrelay_call_ttfb_seconds", buckets,
		metric.WithDescription("Time from forwarding a call down its tunnel to the app's response headers, by outcome and kind.")))
	hold := must(m.Float64Histogram("tsrelay_call_hold_seconds", buckets,
		metric.WithDescription("Time held calls waited for a tunnel, by outcome.")))
	must(m.Int64ObservableGauge("tsrelay_tunnels", metric.WithDescription("Attached tunnels."),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(int64(len(s.tunnels.Snapshot())))
			return nil
		})))
	must(m.Int64ObservableGauge("tsrelay_hooks", metric.WithDescription("Hooks, by whether they are ephemeral."),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			n := map[bool]int64{true: 0, false: 0}
			for _, h := range s.hooks.ListAll() {
				n[h.Ephemeral]++
			}
			for eph, v := range n {
				o.Observe(v, metric.WithAttributes(attribute.Bool("ephemeral", eph)))
			}
			return nil
		})))
	must(m.Int64ObservableGauge("tsrelay_held_calls", metric.WithDescription("Calls waiting for a tunnel to attach."),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(s.relay.Held())
			return nil
		})))

	return func(event string, attrs []slog.Attr) {
		ctx := context.Background()
		switch event {
		case audit.Call:
			var outcome, kind string
			var in, out int64
			var waited, total, firstByte time.Duration
			for _, a := range attrs {
				switch k := a.Value.Kind(); {
				case a.Key == "error" && k == slog.KindString:
					outcome = a.Value.String()
				case a.Key == "kind" && k == slog.KindString:
					kind = a.Value.String()
				case a.Key == "bytesIn" && k == slog.KindInt64:
					in = a.Value.Int64()
				case a.Key == "bytesOut" && k == slog.KindInt64:
					out = a.Value.Int64()
				case a.Key == "waited" && k == slog.KindDuration:
					waited = a.Value.Duration()
				case a.Key == "duration" && k == slog.KindDuration:
					total = a.Value.Duration()
				case a.Key == "ttfb" && k == slog.KindDuration:
					firstByte = a.Value.Duration()
				}
			}
			outcome = cmp.Or(outcome, "ok")
			o := attribute.String("outcome", outcome)
			calls.Add(ctx, 1, metric.WithAttributes(o))
			callBytes.Add(ctx, in, metric.WithAttributes(attribute.String("direction", "in")))
			callBytes.Add(ctx, out, metric.WithAttributes(attribute.String("direction", "out")))
			ok := metric.WithAttributes(o, attribute.String("kind", kind))
			duration.Record(ctx, total.Seconds(), ok)
			if hasAttr(attrs, "ttfb") {
				ttfb.Record(ctx, firstByte.Seconds(), ok)
			}
			if waited > 0 {
				hold.Record(ctx, waited.Seconds(), metric.WithAttributes(o))
			}
		case audit.AuthzDeny:
			denials.Add(ctx, 1, metric.WithAttributes(attribute.String("action", attrString(attrs, "action"))))
		}
	}
}

// must returns v, or panics on err: instrument creation fails only for
// invalid names, which are constants here.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func hasAttr(attrs []slog.Attr, key string) bool {
	for _, a := range attrs {
		if a.Key == key {
			return true
		}
	}
	return false
}

func attrString(attrs []slog.Attr, key string) string {
	for _, a := range attrs {
		if a.Key == key && a.Value.Kind() == slog.KindString {
			return a.Value.String()
		}
	}
	return ""
}
