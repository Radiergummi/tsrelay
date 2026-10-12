package server

import (
	"cmp"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/metrics"
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
		s.metrics.ServeHTTP(w, r)
	default:
		io.WriteString(w, "ok\n")
	}
}

// newMetrics builds the relay's metrics (DESIGN.md §13). Counters derive
// from audit events through the returned observer, so the request path
// has no instrumentation of its own; gauges are read at scrape time.
func (s *Server) newMetrics() (*metrics.Registry, func(event string, attrs []slog.Attr)) {
	m := metrics.NewRegistry()
	calls := m.Counter("tsrelay_calls_total", "Callback calls, by outcome: ok or the relay's error code.", "outcome")
	callBytes := m.Counter("tsrelay_call_bytes_total", "Callback body bytes, in from senders and out to them.", "direction")
	denials := m.Counter("tsrelay_authz_denials_total", "Requests refused by authorization, by action.", "action")
	m.GaugeFunc("tsrelay_tunnels", "Attached tunnels.", "", func() map[string]float64 {
		return map[string]float64{"": float64(len(s.tunnels.Snapshot()))}
	})
	m.GaugeFunc("tsrelay_hooks", "Hooks, by whether they are ephemeral.", "ephemeral", func() map[string]float64 {
		n := map[string]float64{"true": 0, "false": 0}
		for _, h := range s.hooks.ListAll() {
			n[strconv.FormatBool(h.Ephemeral)]++
		}
		return n
	})
	m.GaugeFunc("tsrelay_held_calls", "Calls waiting for a tunnel to attach.", "", func() map[string]float64 {
		return map[string]float64{"": float64(s.relay.Held())}
	})
	observe := func(event string, attrs []slog.Attr) {
		switch event {
		case audit.Call:
			calls.Add(cmp.Or(attrString(attrs, "error"), "ok"), 1)
			callBytes.Add("in", float64(attrInt(attrs, "bytesIn")))
			callBytes.Add("out", float64(attrInt(attrs, "bytesOut")))
		case audit.AuthzDeny:
			denials.Add(attrString(attrs, "action"), 1)
		}
	}
	return m, observe
}

func attrString(attrs []slog.Attr, key string) string {
	for _, a := range attrs {
		if a.Key == key && a.Value.Kind() == slog.KindString {
			return a.Value.String()
		}
	}
	return ""
}

func attrInt(attrs []slog.Attr, key string) int64 {
	for _, a := range attrs {
		if a.Key == key && a.Value.Kind() == slog.KindInt64 {
			return a.Value.Int64()
		}
	}
	return 0
}
