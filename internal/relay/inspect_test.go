package relay

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"tailscale.com/tailcfg"
)

// finished waits until the call has an outcome and returns it.
func (e *env) finished(resp *http.Response) inspect.Call {
	e.t.Helper()
	id := resp.Header.Get(HeaderCallID)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if c, ok := e.in.Get(id); ok && c.Outcome != inspect.OutcomePending {
			return c
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("call %s was not recorded as finished", id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRecordsCall(t *testing.T) {
	e := newEnv(t)
	e.patch(hooks.SettingsPatch{Capture: hooks.Opt[bool]{Set: true, Value: new(true)}})
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		w.Header().Set("Set-Cookie", "session=s3cr3t")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, "created")
	}), tunnel.Options{})
	resp := e.do(sender, "POST", "/~moritz/cb/done?token=t0k3n", strings.NewReader(`{"a":1}`), http.Header{
		"Authorization": {"Bearer s3cr3t"},
		"X-Custom":      {"kept"},
	})
	io.ReadAll(resp.Body)
	c := e.finished(resp)

	if c.HookID != e.hook.ID || c.Kind != inspect.KindSender || c.Outcome != inspect.OutcomeOK || c.Status != 201 {
		t.Fatalf("call = %+v", c)
	}
	if c.Method != "POST" || c.Path != "/done" || c.Query != "token=t0k3n" {
		t.Fatalf("request line = %s %s ? %s", c.Method, c.Path, c.Query)
	}
	if c.Sender.Node != "worker" || !slices.Equal(c.Sender.Tags, []string{"tag:prod-worker"}) || c.Sender.IP == "" {
		t.Fatalf("sender = %+v", c.Sender)
	}
	if c.RequestHeaders.Get("Authorization") != "" || c.RequestHeaders.Get("X-Custom") != "kept" || !slices.Equal(c.Redacted, []string{"Authorization"}) {
		t.Fatalf("request headers = %v, redacted %v", c.RequestHeaders, c.Redacted)
	}
	if string(c.RequestBody.Body) != `{"a":1}` || !c.RequestBody.Complete() {
		t.Fatalf("request body = %+v", c.RequestBody)
	}
	if len(c.Attempts) != 1 {
		t.Fatalf("attempts = %+v", c.Attempts)
	}
	a := c.Attempts[0]
	if a.Status != 201 || a.TunnelID == "" || a.Error != "" || a.BytesIn != 7 || a.BytesOut != 7 {
		t.Fatalf("attempt = %+v", a)
	}
	if a.ResponseHeaders.Get("Set-Cookie") != "" || !slices.Equal(a.Redacted, []string{"Set-Cookie"}) || string(a.ResponseBody.Body) != "created" {
		t.Fatalf("response = %v %v %+v", a.ResponseHeaders, a.Redacted, a.ResponseBody)
	}
	if s := e.in.List(e.hook.ID); len(s) != 1 || s[0].ID != c.ID {
		t.Fatalf("list = %+v", s)
	}
}

// Capture never changes what is forwarded: a body over the capture limit
// still arrives in full and byte-exact.
func TestCaptureLimitKeepsForwardingExact(t *testing.T) {
	e := newEnvWith(t, inspect.Config{CaptureLimit: 1024})
	e.patch(hooks.SettingsPatch{Capture: hooks.Opt[bool]{Set: true, Value: new(true)}})
	payload := make([]byte, 1<<20)
	rand.Read(payload)
	want := sha256.Sum256(payload)
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if sha256.Sum256(b) != want {
			t.Error("the forwarded body changed")
		}
		w.Write(b)
	}), tunnel.Options{})
	resp := e.do(sender, "POST", "/~moritz/cb", bytes.NewReader(payload), nil)
	got, _ := io.ReadAll(resp.Body)
	if sha256.Sum256(got) != want {
		t.Fatal("the response body changed")
	}
	c := e.finished(resp)
	if rb := c.RequestBody; len(rb.Body) != 1024 || rb.Size != 1<<20 || !rb.Truncated || rb.Complete() {
		t.Fatalf("request capture: %d bytes kept, size %d, truncated %v", len(rb.Body), rb.Size, rb.Truncated)
	}
	if rb := c.Attempts[0].ResponseBody; len(rb.Body) != 1024 || rb.Size != 1<<20 || !rb.Truncated {
		t.Fatalf("response capture: %d bytes kept, size %d", len(rb.Body), rb.Size)
	}
}

// A query over the capture limit is kept cut short and marked, so the
// inspector shows it is incomplete and a replay can refuse it.
func TestCaptureLimitCutsQuery(t *testing.T) {
	e := newEnvWith(t, inspect.Config{CaptureLimit: 8})
	e.attach(echo(), tunnel.Options{})
	for query, want := range map[string]struct {
		kept string
		cut  bool
	}{"a=1": {"a=1", false}, "token=0123456789": {"token=01", true}} {
		resp := e.do(sender, "GET", "/~moritz/cb?"+query, nil, nil)
		io.ReadAll(resp.Body)
		if c := e.finished(resp); c.Query != want.kept || c.QueryTruncated != want.cut {
			t.Errorf("query %q: kept %q, truncated %v; want %q, %v", query, c.Query, c.QueryTruncated, want.kept, want.cut)
		}
	}
}

func TestCaptureOff(t *testing.T) {
	e := newEnv(t)
	e.attach(echo(), tunnel.Options{})
	e.patch(hooks.SettingsPatch{Capture: hooks.Opt[bool]{Set: true, Value: new(false)}})
	resp := e.do(sender, "POST", "/~moritz/cb", strings.NewReader("secret body"), http.Header{"X-Custom": {"kept"}})
	io.ReadAll(resp.Body)
	c := e.finished(resp)
	if c.RequestBody.Body != nil || c.RequestBody.Captured || c.RequestBody.Size != 11 || c.RequestHeaders.Get("X-Custom") != "kept" {
		t.Fatalf("hook capture=false: %+v %v", c.RequestBody, c.RequestHeaders)
	}

	// The relay-wide switch wins over a hook that captures (the default).
	off := newEnvWith(t, inspect.Config{NoBodies: true})
	off.attach(echo(), tunnel.Options{})
	resp = off.do(sender, "POST", "/~moritz/cb", strings.NewReader("secret body"), nil)
	io.ReadAll(resp.Body)
	if c := off.finished(resp); c.RequestBody.Body != nil || c.Attempts[0].ResponseBody.Body != nil {
		t.Fatalf("--capture-bodies=false: %+v", c)
	}
}

func TestRecordsRefusedCalls(t *testing.T) {
	e := newEnv(t)
	e.patch(hooks.SettingsPatch{HoldTimeout: hooks.Opt[hooks.Duration]{Set: true, Value: new(hooks.Duration(0))}})

	// No tunnel: delivery started and failed, so there is one attempt. The
	// body was never read, so it cannot be replayed.
	resp := e.do(sender, "POST", "/~moritz/cb", strings.NewReader("{}"), nil)
	wantProblem(t, resp, 561, problem.TunnelUnavailable)
	c := e.finished(resp)
	if c.Outcome != problem.TunnelUnavailable || c.Status != 561 || len(c.Attempts) != 1 ||
		c.Attempts[0].Error != problem.TunnelUnavailable || c.Attempts[0].TunnelID != "" || c.RequestBody.Complete() {
		t.Fatalf("held call = %+v", c)
	}

	// Denied by channel: recorded on the hook for its owner, without
	// attempts, while the sender gets the same 404 as for a missing hook.
	staging := identify.Tagged("w2", []string{"tag:staging"}, `{"roles":["sender"],"channels":["staging"]}`)
	resp = e.do(staging, "POST", "/~moritz/cb", nil, nil)
	wantProblem(t, resp, 404, problem.HookNotFound)
	c = e.finished(resp)
	if c.HookID != e.hook.ID || c.Outcome != problem.HookNotFound || len(c.Attempts) != 0 || c.Sender.Node != "w2" {
		t.Fatalf("denied call = %+v", c)
	}

	// A path that matches no hook is not recorded anywhere.
	resp = e.do(sender, "POST", "/~moritz/nope", nil, nil)
	if _, ok := e.in.Get(resp.Header.Get(HeaderCallID)); ok {
		t.Fatal("a call for no hook was recorded")
	}
}

// A node with a sender role but not the hook's channel cannot flood the
// owner's inspector: its refused calls stay apart from delivered ones,
// publish no events and spend none of the hook's rate limit.
func TestRefusedCallsDoNotEvict(t *testing.T) {
	e := newEnvWith(t, inspect.Config{CallsPerHook: 5})
	var events atomic.Int64
	e.in.OnChange = func(tailcfg.UserID, string, inspect.Summary) { events.Add(1) }
	e.patch(hooks.SettingsPatch{RateLimit: hooks.Opt[hooks.RateLimit]{Set: true, Value: &hooks.RateLimit{PerSecond: 0.001, Burst: 6}}})
	e.attach(echo(), tunnel.Options{})
	var delivered []string
	for range 5 {
		resp := e.do(sender, "POST", "/~moritz/cb", nil, nil)
		io.ReadAll(resp.Body)
		delivered = append(delivered, e.finished(resp).ID)
	}
	before := events.Load()

	staging := identify.Tagged("w2", []string{"tag:staging"}, `{"roles":["sender"],"channels":["staging"]}`)
	var last inspect.Call
	for range 50 {
		resp := e.do(staging, "POST", "/~moritz/cb", nil, nil)
		wantProblem(t, resp, 404, problem.HookNotFound)
		last = e.finished(resp) // the owner can still open a refused call
	}
	if !last.Refused || last.HookID != e.hook.ID {
		t.Fatalf("refused call = %+v", last)
	}
	if n := events.Load(); n != before {
		t.Fatalf("refused calls published %d events", n-before)
	}

	refused, kept := 0, map[string]bool{}
	for _, s := range e.in.List(e.hook.ID) {
		if s.Refused {
			refused++
		} else {
			kept[s.ID] = true
		}
	}
	for _, id := range delivered {
		if !kept[id] {
			t.Fatalf("delivered call %s was evicted by refused ones", id)
		}
	}
	if refused == 0 || refused > 10 {
		t.Fatalf("%d refused calls listed, want 1 to 10", refused)
	}

	// The refused calls spent none of the burst: the sixth real call passes.
	if resp := e.do(sender, "POST", "/~moritz/cb", nil, nil); resp.StatusCode != 200 {
		t.Fatalf("next real call: %d", resp.StatusCode)
	}
}
