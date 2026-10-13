package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/telemetry"
)

// tracedEnv is newEnv with tracing on, recording spans instead of
// exporting them.
func tracedEnv(t *testing.T, cfg Config) (*env, *tracetest.SpanRecorder) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tel := telemetry.Must(telemetry.Setup(context.Background(), telemetry.Config{}))
	tel.Tracer = sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	tel.Tracing = true
	cfg.Telemetry = tel
	return newEnv(t, cfg), rec
}

// spanNamed waits for an ended span with that name and returns the first one.
func (e *env) spanNamed(rec *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	e.t.Helper()
	var found sdktrace.ReadOnlySpan
	e.waitFor(func() bool {
		for _, s := range rec.Ended() {
			if s.Name() == name {
				found = s
				return true
			}
		}
		return false
	})
	return found
}

func attr(s sdktrace.ReadOnlySpan, key string) (string, bool) {
	for _, a := range s.Attributes() {
		if string(a.Key) == key {
			return a.Value.Emit(), true
		}
	}
	return "", false
}

func hasEvent(s sdktrace.ReadOnlySpan, name string) bool {
	for _, ev := range s.Events() {
		if ev.Name == name {
			return true
		}
	}
	return false
}

// traceApp is a local app that reports the trace headers it received.
func traceApp(t *testing.T) (url string, got <-chan http.Header) {
	ch := make(chan http.Header, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		ch <- http.Header{"Traceparent": r.Header.Values("Traceparent"), "Tracestate": r.Header.Values("Tracestate")}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, ch
}

const (
	senderTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	senderSpanID  = "00f067aa0ba902b7"
	senderParent  = "00-" + senderTraceID + "-" + senderSpanID + "-01"
	senderState   = "vendor=opaque"
)

func senderTraceHeaders() http.Header {
	return http.Header{"Traceparent": {senderParent}, "Tracestate": {senderState}}
}

func TestCallSpans(t *testing.T) {
	e, rec := tracedEnv(t, Config{})
	app, _ := traceApp(t)
	h, _ := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	defer e.cli(h.ID, app)()
	resp := e.send(h, "/done?token=x", []byte("hi"), nil)
	io.ReadAll(resp.Body)

	call := e.spanNamed(rec, "tsrelay.call")
	auth := e.spanNamed(rec, "tsrelay.authorize")
	fwd := e.spanNamed(rec, "tsrelay.forward")
	if call.SpanKind() != trace.SpanKindServer || fwd.SpanKind() != trace.SpanKindClient {
		t.Errorf("kinds = %v, %v", call.SpanKind(), fwd.SpanKind())
	}
	for _, child := range []sdktrace.ReadOnlySpan{auth, fwd} {
		if child.Parent().SpanID() != call.SpanContext().SpanID() {
			t.Errorf("%s is not a child of tsrelay.call", child.Name())
		}
	}
	if id, _ := attr(call, "tsrelay.call.id"); id != resp.Header.Get("Tsrelay-Call-Id") {
		t.Errorf("tsrelay.call.id = %q, want %q", id, resp.Header.Get("Tsrelay-Call-Id"))
	}
	for key, want := range map[string]string{
		"url.path":                  "/~moritz/cb/done",
		"http.request.method":       "POST",
		"http.response.status_code": "200",
		"tsrelay.call.kind":         "sender",
		"tsrelay.hook.id":           h.ID,
		"tsrelay.sender.node":       "worker",
		"tsrelay.bytes_in":          "2",
	} {
		if got, _ := attr(call, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if _, ok := attr(call, "tsrelay.error"); ok {
		t.Error("tsrelay.error set on a successful call")
	}
	if call.Status().Code == codes.Error {
		t.Error("successful call marked as an error")
	}
	if !hasEvent(call, "routed") {
		t.Error("no routed event")
	}
}

func TestRefusedCallSpans(t *testing.T) {
	e, rec := tracedEnv(t, Config{})
	h, _ := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	req, _ := http.NewRequest("POST", h.URL, nil)
	resp, err := e.fake.Client(receiver).Do(req) // no sender role
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	call := e.spanNamed(rec, "tsrelay.call")
	if call.Status().Code != codes.Error {
		t.Errorf("status = %v, want Error", call.Status())
	}
	if got, _ := attr(call, "tsrelay.error"); got != "sender_forbidden" {
		t.Errorf("tsrelay.error = %q", got)
	}
	e.spanNamed(rec, "tsrelay.authorize")
}

// An app's own 5xx is the app's answer, not a relay error.
func TestAppErrorIsNotASpanError(t *testing.T) {
	e, rec := tracedEnv(t, Config{})
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer app.Close()
	h, _ := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	defer e.cli(h.ID, app.URL)()
	io.ReadAll(e.send(h, "", nil, nil).Body)
	call := e.spanNamed(rec, "tsrelay.call")
	if call.Status().Code == codes.Error {
		t.Error("the app's 503 marked the call span as an error")
	}
	if got, _ := attr(call, "http.response.status_code"); got != "503" {
		t.Errorf("status code = %q", got)
	}
}

func TestHeldCallSpan(t *testing.T) {
	e, rec := tracedEnv(t, Config{})
	app, _ := traceApp(t)
	h, _ := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	done := make(chan struct{})
	go func() {
		defer close(done)
		req, _ := http.NewRequest("POST", h.URL, nil)
		if resp, err := e.fake.Client(sender).Do(req); err == nil {
			resp.Body.Close()
		}
	}()
	e.waitFor(func() bool { return e.srv.relay.Held() == 1 })
	defer e.cli(h.ID, app)()
	<-done
	hold := e.spanNamed(rec, "tsrelay.hold")
	if got, _ := attr(hold, "tsrelay.hold.result"); got != "attached" {
		t.Errorf("tsrelay.hold.result = %q, want attached", got)
	}
}

func TestHoldTimeoutSpan(t *testing.T) {
	e, rec := tracedEnv(t, Config{})
	short := hooks.Duration(50 * time.Millisecond)
	h, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb", Settings: hooks.Settings{HoldTimeout: &short}})
	if err != nil {
		t.Fatal(err)
	}
	resp := e.send(h, "", nil, nil)
	if resp.StatusCode != 503 {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	hold := e.spanNamed(rec, "tsrelay.hold")
	if got, _ := attr(hold, "tsrelay.hold.result"); got != "timeout" {
		t.Errorf("tsrelay.hold.result = %q, want timeout", got)
	}
	if call := e.spanNamed(rec, "tsrelay.call"); call.Status().Code != codes.Error {
		t.Error("tunnel_unavailable not marked as an error")
	}
}

func TestTracePropagationOn(t *testing.T) {
	e, rec := tracedEnv(t, Config{})
	app, got := traceApp(t)
	h, _ := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	defer e.cli(h.ID, app)()
	io.ReadAll(e.send(h, "", nil, senderTraceHeaders()).Body)

	call := e.spanNamed(rec, "tsrelay.call")
	fwd := e.spanNamed(rec, "tsrelay.forward")
	if call.SpanContext().TraceID().String() != senderTraceID || call.Parent().SpanID().String() != senderSpanID {
		t.Fatalf("call span did not join the sender's trace: trace %s parent %s", call.SpanContext().TraceID(), call.Parent().SpanID())
	}
	hdr := <-got
	want := "00-" + senderTraceID + "-" + fwd.SpanContext().SpanID().String() + "-01"
	if tp := hdr.Get("Traceparent"); tp != want {
		t.Errorf("app got traceparent %q, want %q (parent tsrelay.forward)", tp, want)
	}
	if ts := hdr.Get("Tracestate"); ts != senderState {
		t.Errorf("app got tracestate %q, want the sender's %q carried on", ts, senderState)
	}
}

func TestTracePropagationOff(t *testing.T) {
	e, rec := tracedEnv(t, Config{NoTracePropagation: true})
	app, got := traceApp(t)
	h, _ := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	defer e.cli(h.ID, app)()
	io.ReadAll(e.send(h, "", nil, senderTraceHeaders()).Body)

	if call := e.spanNamed(rec, "tsrelay.call"); call.SpanContext().TraceID().String() == senderTraceID {
		t.Error("call span joined the sender's trace with propagation off")
	}
	if hdr := <-got; hdr.Get("Traceparent") != senderParent || hdr.Get("Tracestate") != senderState {
		t.Errorf("app got %v, want the sender's headers untouched", hdr)
	}
}

func TestNoTracingForwardsTraceHeaders(t *testing.T) {
	e := newEnv(t, Config{})
	app, got := traceApp(t)
	h, _ := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	defer e.cli(h.ID, app)()
	io.ReadAll(e.send(h, "", nil, senderTraceHeaders()).Body)
	if hdr := <-got; hdr.Get("Traceparent") != senderParent || hdr.Get("Tracestate") != senderState {
		t.Errorf("app got %v, want the sender's headers untouched", hdr)
	}
}

func TestMalformedTraceparent(t *testing.T) {
	e, rec := tracedEnv(t, Config{})
	app, got := traceApp(t)
	h, _ := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	defer e.cli(h.ID, app)()
	for _, bad := range []string{"garbage", "ff-" + senderTraceID + "-" + senderSpanID + "-01", "00-00000000000000000000000000000000-0000000000000000-01"} {
		resp := e.send(h, "", nil, http.Header{"Traceparent": {bad}})
		io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("traceparent %q: status %d", bad, resp.StatusCode)
		}
		hdr := <-got
		if tp := hdr.Get("Traceparent"); len(tp) != 55 || tp == bad {
			t.Errorf("traceparent %q: app got %q, want a new valid one", bad, tp)
		}
	}
	callSpans := func() (calls []sdktrace.ReadOnlySpan) {
		for _, s := range rec.Ended() {
			if s.Name() == "tsrelay.call" {
				calls = append(calls, s)
			}
		}
		return
	}
	e.waitFor(func() bool { return len(callSpans()) >= 3 })
	for _, s := range callSpans() {
		if s.Parent().IsValid() {
			t.Errorf("call span has parent %v from a malformed traceparent", s.Parent())
		}
	}
}

func TestBodyTooLargeEvent(t *testing.T) {
	e, rec := tracedEnv(t, Config{})
	max := int64(4)
	h, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb", Settings: hooks.Settings{MaxBodyBytes: &max}})
	if err != nil {
		t.Fatal(err)
	}
	resp := e.send(h, "", []byte("way too long"), nil)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
	call := e.spanNamed(rec, "tsrelay.call")
	if !hasEvent(call, "body_too_large") {
		t.Error("no body_too_large event")
	}
	if call.Status().Code != codes.Error {
		t.Error("body_too_large call not marked as an error")
	}
	if got, _ := attr(call, "tsrelay.error"); got != "body_too_large" {
		t.Errorf("tsrelay.error = %q, want body_too_large", got)
	}
}

func TestTunnelSpans(t *testing.T) {
	e, rec := tracedEnv(t, Config{})
	app, _ := traceApp(t)
	h, _ := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	stop := e.cli(h.ID, app)
	attach := e.spanNamed(rec, "tsrelay.tunnel.attach")
	if got, _ := attr(attach, "tsrelay.tunnel.result"); got != "attached" {
		t.Errorf("tsrelay.tunnel.result = %q", got)
	}
	if got, _ := attr(attach, "tsrelay.hook.ids"); got != `["`+h.ID+`"]` {
		t.Errorf("tsrelay.hook.ids = %q", got)
	}
	stop()
	detach := e.spanNamed(rec, "tsrelay.tunnel.detach")
	if got, _ := attr(detach, "tsrelay.tunnel.reason"); got != "disconnected" {
		t.Errorf("tsrelay.tunnel.reason = %q", got)
	}
	if detach.SpanContext().TraceID() == attach.SpanContext().TraceID() {
		t.Error("detach shares the attach trace; want its own trace, linked")
	}
	if links := detach.Links(); len(links) != 1 || links[0].SpanContext.SpanID() != attach.SpanContext().SpanID() {
		t.Errorf("detach links = %v, want the attach span", links)
	}
}

func TestReplayParentedByAPISpan(t *testing.T) {
	e, rec := tracedEnv(t, Config{})
	app, _ := traceApp(t)
	h, _ := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	defer e.cli(h.ID, app)()
	resp := e.send(h, "", []byte("x"), nil)
	io.ReadAll(resp.Body)
	callID := resp.Header.Get("Tsrelay-Call-Id")
	e.waitFor(func() bool {
		var c inspect.Call
		return e.getJSON("/api/v1/hooks/"+h.ID+"/calls/"+callID, &c) == 200 && c.Outcome == inspect.OutcomeOK
	})
	if code := e.postJSON("/api/v1/hooks/"+h.ID+"/calls/"+callID+"/replay", nil, nil); code != 201 {
		t.Fatalf("replay = %d", code)
	}
	apiSpan := e.spanNamed(rec, "POST /api/v1/hooks/{id}/calls/{callID}/replay")
	var replay sdktrace.ReadOnlySpan
	e.waitFor(func() bool {
		for _, s := range rec.Ended() {
			if k, _ := attr(s, "tsrelay.call.kind"); s.Name() == "tsrelay.call" && k == "replay" {
				replay = s
				return true
			}
		}
		return false
	})
	if replay.Parent().SpanID() != apiSpan.SpanContext().SpanID() {
		t.Error("replayed call is not a child of the API request span")
	}
	if got, _ := attr(apiSpan, "tsrelay.actor.login"); got != "moritz@example.com" {
		t.Errorf("tsrelay.actor.login = %q", got)
	}
	if got, _ := attr(apiSpan, "http.route"); got != "/api/v1/hooks/{id}/calls/{callID}/replay" {
		t.Errorf("http.route = %q", got)
	}
	if _, ok := e.findSpan(rec, "tsrelay.authorize", replay); ok {
		t.Error("a replay was authorized as a sender call")
	}
}

// findSpan returns an ended span with that name whose parent is parent.
func (e *env) findSpan(rec *tracetest.SpanRecorder, name string, parent sdktrace.ReadOnlySpan) (sdktrace.ReadOnlySpan, bool) {
	for _, s := range rec.Ended() {
		if s.Name() == name && s.Parent().SpanID() == parent.SpanContext().SpanID() {
			return s, true
		}
	}
	return nil, false
}

func TestAPISpansSkipStreams(t *testing.T) {
	e, rec := tracedEnv(t, Config{})
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", e.base+"/api/v1/events", nil)
	resp, err := e.api.HTTP.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	resp.Body.Close()
	e.getJSON("/api/v1/me", nil) // a traced request, to know spans are flowing
	e.spanNamed(rec, "GET /api/v1/me")
	for _, s := range rec.Started() {
		if strings.Contains(s.Name(), "/events") {
			t.Errorf("event stream got span %q", s.Name())
		}
	}
	ops := &Ops{}
	ops.Ready(e.srv)
	m := httptest.NewRecorder()
	ops.ServeHTTP(m, httptest.NewRequest("GET", "/metrics", nil))
	if strings.Contains(m.Body.String(), `route="/api/v1/events"`) {
		t.Error("the event stream is in the API latency histogram")
	}
	if !strings.Contains(m.Body.String(), `tsrelay_api_request_duration_seconds_count{method="GET",route="/api/v1/me",status="200"} 1`) {
		t.Errorf("no API latency for /me:\n%s", m.Body.String())
	}
}
