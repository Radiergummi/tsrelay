package relay

import (
	"context"
	"net/http"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// With tracing off (no Tracer), the span helpers the handler runs on every
// call must not allocate (DESIGN.md §13: negligible overhead when off).
func TestTracingOffAllocatesNothing(t *testing.T) {
	h := &Handler{}
	header := http.Header{"Traceparent": {"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}}
	ctx := context.Background()
	a := callAttrs{callID: "c_x", kind: "sender", method: "POST", path: "/~m/x", status: 200}
	allocs := testing.AllocsPerRun(100, func() {
		ct := h.startCall(ctx, header, false)
		at := ct.child(h.Tracer, "tsrelay.authorize")
		at.end(attribute.KeyValue{}, false)
		ct.event("routed")
		ht := ct.child(h.Tracer, "tsrelay.hold")
		ht.end(attribute.String("tsrelay.hold.result", "attached"), true)
		ct.finish(a)
	})
	if allocs != 0 {
		t.Fatalf("span helpers with tracing off allocate %v times per call, want 0", allocs)
	}
	var headers time.Time
	resp := &http.Response{} // shared, so returning it doesn't itself allocate per run
	ft := forwardTransport{rt: roundTripFunc(func(*http.Request) (*http.Response, error) { return resp, nil }), headers: &headers}
	req, _ := http.NewRequest("GET", "http://x/", nil)
	if n := testing.AllocsPerRun(100, func() { ft.RoundTrip(req) }); n != 0 {
		t.Fatalf("forwardTransport with tracing off allocates %v times, want 0", n)
	}
}
