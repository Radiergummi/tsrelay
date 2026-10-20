package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/ui"
)

func TestOps(t *testing.T) {
	e := newEnv(t, Config{})
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(204)
	}))
	defer local.Close()
	hook, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.cli(hook.ID, local.URL)()

	ops := &Ops{}
	get := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		ops.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec.Code, rec.Body.String()
	}
	if code, _ := get("/healthz"); code != 200 {
		t.Fatalf("/healthz before ready = %d, want 200 (liveness while tsnet starts)", code)
	}
	for _, p := range []string{"/readyz", "/metrics"} {
		if code, _ := get(p); code != 503 {
			t.Fatalf("%s before ready = %d, want 503", p, code)
		}
	}
	ops.Ready(e.srv)
	if code, _ := get("/readyz"); code != 200 {
		t.Fatalf("/readyz = %d", code)
	}
	if code, _ := get("/nope"); code != 404 {
		t.Fatalf("/nope = %d", code)
	}

	e.send(hook, "", []byte("hello"), nil) // delivered
	req, _ := http.NewRequest("POST", hook.URL, nil)
	resp, err := e.fake.Client(receiver).Do(req) // a receiver is not a sender: denied
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Call records are written after the response, so wait for them.
	var body string
	e.waitFor(func() bool {
		_, body = get("/metrics")
		return strings.Contains(body, `tsrelay_calls_total{outcome="ok"} 1`) &&
			strings.Contains(body, `tsrelay_calls_total{outcome="sender_forbidden"} 1`)
	})
	for _, want := range []string{
		`tsrelay_call_bytes_total{direction="in"} 5`,
		`tsrelay_authz_denials_total{action="call"} 1`,
		"\ntsrelay_tunnels 1\n",
		`tsrelay_hooks{ephemeral="false"} 1`,
		`tsrelay_hooks{ephemeral="true"} 0`,
		"\ntsrelay_held_calls 0\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q:\n%s", want, body)
		}
	}
	for _, want := range []string{
		"# TYPE tsrelay_calls_total counter",
		"# TYPE tsrelay_call_bytes_total counter",
		"# TYPE tsrelay_authz_denials_total counter",
		"# TYPE tsrelay_tunnels gauge",
		"# TYPE tsrelay_hooks gauge",
		"# TYPE tsrelay_held_calls gauge",
		"# TYPE tsrelay_call_duration_seconds histogram",
		`tsrelay_call_duration_seconds_count{kind="sender",outcome="ok"} 1`,
		`tsrelay_call_duration_seconds_count{kind="sender",outcome="sender_forbidden"} 1`,
		`tsrelay_call_ttfb_seconds_count{kind="sender",outcome="ok"} 1`,
		`tsrelay_call_duration_seconds_bucket{kind="sender",outcome="ok",le="120"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q:\n%s", want, body)
		}
	}
	// A refused call never reached an app: no TTFB. Nothing was held.
	for _, bad := range []string{`tsrelay_call_ttfb_seconds_count{kind="sender",outcome="sender_forbidden"}`, "tsrelay_call_hold_seconds_count"} {
		if strings.Contains(body, bad) {
			t.Errorf("metrics contain %q:\n%s", bad, body)
		}
	}
}

func TestOpsPprof(t *testing.T) {
	e := newEnv(t, Config{})
	get := func(h http.Handler, path string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec.Code
	}
	off := &Ops{}
	off.Ready(e.srv)
	on := &Ops{Pprof: true}
	on.Ready(e.srv)
	for _, p := range []string{"/debug/pprof/", "/debug/pprof/heap", "/debug/pprof/cmdline", "/debug/pprof/symbol"} {
		if code := get(off, p); code != 404 {
			t.Errorf("%s without --ops-pprof = %d, want 404", p, code)
		}
		if code := get(on, p); code != 200 {
			t.Errorf("%s with --ops-pprof = %d, want 200", p, code)
		}
	}
	// pprof is never reachable through the relay's tailnet handler.
	req, _ := http.NewRequest("GET", e.base+"/debug/pprof/", nil)
	resp, err := e.fake.Client(receiver).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// The relay's own listener has no pprof route; unmatched GETs fall
	// through to the UI's catch-all, so a 200 there is the app, not pprof.
	if resp.Header.Get("Content-Security-Policy") != ui.CSP {
		t.Fatalf("/debug/pprof/ on the relay's own listener = %d %v, want the UI's app, not pprof", resp.StatusCode, resp.Header)
	}
	// pprof works before the relay is ready, like /healthz: it profiles the process.
	if code := get(&Ops{Pprof: true}, "/debug/pprof/"); code != 200 {
		t.Errorf("/debug/pprof/ before ready = %d, want 200", code)
	}
}

func TestHoldMetrics(t *testing.T) {
	e := newEnv(t, Config{})
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer local.Close()
	hook, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "held"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan int)
	go func() {
		req, _ := http.NewRequest("POST", hook.URL, nil)
		resp, err := e.fake.Client(sender).Do(req)
		if err != nil {
			done <- 0
			return
		}
		resp.Body.Close()
		done <- resp.StatusCode
	}()
	e.waitFor(func() bool { return e.srv.relay.Held() == 1 })
	defer e.cli(hook.ID, local.URL)()
	if code := <-done; code != 204 {
		t.Fatalf("held call = %d, want 204", code)
	}
	ops := &Ops{}
	ops.Ready(e.srv)
	e.waitFor(func() bool {
		rec := httptest.NewRecorder()
		ops.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
		return strings.Contains(rec.Body.String(), `tsrelay_call_hold_seconds_count{outcome="ok"} 1`)
	})
}

// Each way a call can end is counted under its outcome label: delivered,
// refused, held until the hold timed out, and given up by its sender while
// held.
func TestCallMetricsByOutcome(t *testing.T) {
	e := newEnv(t, Config{})
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer local.Close()
	delivered, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "delivered"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.cli(delivered.ID, local.URL)()
	short := hooks.Duration(50 * time.Millisecond)
	unattached, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "unattached", Settings: hooks.Settings{HoldTimeout: &short}})
	if err != nil {
		t.Fatal(err)
	}
	held, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "held"})
	if err != nil {
		t.Fatal(err)
	}
	post := func(ctx context.Context, url string) {
		req, _ := http.NewRequestWithContext(ctx, "POST", url, nil)
		if resp, err := e.fake.Client(sender).Do(req); err == nil {
			resp.Body.Close()
		}
	}
	e.send(delivered, "", nil, nil)
	post(context.Background(), e.base+"/~nobody/nothing")
	post(context.Background(), unattached.URL)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { post(ctx, held.URL); close(done) }()
	e.waitFor(func() bool { return e.srv.relay.Held() == 1 })
	cancel()
	<-done

	ops := &Ops{}
	ops.Ready(e.srv)
	want := []string{
		`tsrelay_calls_total{outcome="ok"} 1`,
		`tsrelay_calls_total{outcome="hook_not_found"} 1`,
		`tsrelay_calls_total{outcome="tunnel_unavailable"} 1`,
		`tsrelay_calls_total{outcome="sender_canceled"} 1`,
		`tsrelay_call_hold_seconds_count{outcome="tunnel_unavailable"} 1`,
		`tsrelay_call_hold_seconds_count{outcome="sender_canceled"} 1`,
		`tsrelay_call_ttfb_seconds_count{kind="sender",outcome="ok"} 1`,
	}
	// Call records are written after the response, so wait for them.
	var missing []string
	defer func() {
		if t.Failed() {
			t.Logf("metrics lack %q", missing)
		}
	}()
	e.waitFor(func() bool {
		rec := httptest.NewRecorder()
		ops.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
		missing = missing[:0]
		for _, w := range want {
			if !strings.Contains(rec.Body.String(), w) {
				missing = append(missing, w)
			}
		}
		return len(missing) == 0
	})
}
