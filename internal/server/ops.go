package server

import (
	"cmp"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Ops serves the ops listener: /healthz, /readyz and /metrics, and with
// Pprof, /debug/pprof/. It exists before the relay is up, so liveness
// probes pass while tsnet joins the tailnet; /readyz and /metrics answer
// 503 until Ready is called.
type Ops struct {
	Pprof bool // serve net/http/pprof under /debug/pprof/; set before serving
	srv   atomic.Pointer[Server]
}

// Ready marks the relay as up: tsnet joined, the store loaded, s serving.
func (o *Ops) Ready(s *Server) { o.srv.Store(s) }

func (o *Ops) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s := o.srv.Load()
	switch p := r.URL.Path; {
	case p == "/healthz":
		io.WriteString(w, "ok\n")
	case strings.HasPrefix(p, "/debug/pprof/") && o.Pprof:
		servePprof(w, r)
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

// servePprof routes to net/http/pprof explicitly, never through
// http.DefaultServeMux, which other packages may also register on.
func servePprof(w http.ResponseWriter, r *http.Request) {
	switch name := strings.TrimPrefix(r.URL.Path, "/debug/pprof/"); name {
	case "cmdline":
		pprof.Cmdline(w, r)
	case "profile":
		pprof.Profile(w, r)
	case "symbol":
		pprof.Symbol(w, r)
	case "trace":
		pprof.Trace(w, r)
	default:
		pprof.Index(w, r) // the index, and named profiles such as heap or goroutine
	}
}

// newMetrics builds the relay's metrics (DESIGN.md §13). Counters and
// histograms derive from audit events through the returned observer, so the
// request path has no instrumentation of its own; gauges are read when the
// metrics are collected.
func (s *Server) newMetrics(mp metric.MeterProvider) func(event string, attrs []slog.Attr) {
	m := mp.Meter(telemetry.ScopeName)
	calls := telemetry.Must(m.Int64Counter("tsrelay_calls_total", metric.WithDescription("Callback calls, by outcome: ok or the relay's error code.")))
	callBytes := telemetry.Must(m.Int64Counter("tsrelay_call_bytes_total", metric.WithDescription("Callback body bytes, in from senders and out to them.")))
	denials := telemetry.Must(m.Int64Counter("tsrelay_authz_denials_total", metric.WithDescription("Requests refused by authorization, by action.")))
	buckets := metric.WithExplicitBucketBoundaries(telemetry.Buckets...)
	duration := telemetry.Must(m.Float64Histogram("tsrelay_call_duration_seconds", buckets,
		metric.WithDescription("Callback calls from arrival to the last byte back, by outcome and kind.")))
	ttfb := telemetry.Must(m.Float64Histogram("tsrelay_call_ttfb_seconds", buckets,
		metric.WithDescription("Time from forwarding a call down its tunnel to the app's response headers, by outcome and kind.")))
	hold := telemetry.Must(m.Float64Histogram("tsrelay_call_hold_seconds", buckets,
		metric.WithDescription("Time held calls waited for a tunnel, by outcome.")))
	telemetry.Must(m.Int64ObservableGauge("tsrelay_tunnels", metric.WithDescription("Attached tunnels."),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(int64(len(s.tunnels.Snapshot())))
			return nil
		})))
	ephemeral := metric.WithAttributes(attribute.Bool("ephemeral", true))
	notEphemeral := metric.WithAttributes(attribute.Bool("ephemeral", false))
	telemetry.Must(m.Int64ObservableGauge("tsrelay_hooks", metric.WithDescription("Hooks, by whether they are ephemeral."),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			var eph, persistent int64
			for _, h := range s.hooks.ListAll() {
				if h.Ephemeral {
					eph++
				} else {
					persistent++
				}
			}
			o.Observe(eph, ephemeral)
			o.Observe(persistent, notEphemeral)
			return nil
		})))
	telemetry.Must(m.Int64ObservableGauge("tsrelay_held_calls", metric.WithDescription("Calls waiting for a tunnel to attach."),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(s.relay.Held())
			return nil
		})))

	bytesIn := metric.WithAttributes(attribute.String("direction", "in"))
	bytesOut := metric.WithAttributes(attribute.String("direction", "out"))
	return func(event string, attrs []slog.Attr) {
		ctx := context.Background()
		switch event {
		case audit.Call:
			var outcome, kind string
			var in, out int64
			var waited, total, firstByte time.Duration
			hasTTFB := false // the app's response headers arrived
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
					firstByte, hasTTFB = a.Value.Duration(), true
				}
			}
			outcome = cmp.Or(outcome, "ok")
			o := attribute.String("outcome", outcome)
			byOutcome := metric.WithAttributes(o)
			calls.Add(ctx, 1, byOutcome)
			callBytes.Add(ctx, in, bytesIn)
			callBytes.Add(ctx, out, bytesOut)
			ok := metric.WithAttributes(o, attribute.String("kind", kind))
			duration.Record(ctx, total.Seconds(), ok)
			if hasTTFB {
				ttfb.Record(ctx, firstByte.Seconds(), ok)
			}
			if waited > 0 {
				hold.Record(ctx, waited.Seconds(), byOutcome)
			}
		case audit.AuthzDeny:
			denials.Add(ctx, 1, metric.WithAttributes(attribute.String("action", attrString(attrs, "action"))))
		}
	}
}

func attrString(attrs []slog.Attr, key string) string {
	for _, a := range attrs {
		if a.Key == key && a.Value.Kind() == slog.KindString {
			return a.Value.String()
		}
	}
	return ""
}
