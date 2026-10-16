package relay

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"github.com/Radiergummi/tsrelay/internal/tunnel/tunneltest"
)

var (
	receiver = identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"]}`)
	sender   = identify.Tagged("worker", []string{"tag:prod-worker"}, `{"roles":["sender"]}`)
)

type env struct {
	t      *testing.T
	fake   *identify.Fake
	reg    *tunnel.Registry
	mgr    *hooks.Manager
	in     *inspect.Inspector
	h      *Handler
	hook   hooks.Hook
	srv    *httptest.Server
	logMu  sync.Mutex
	logBuf bytes.Buffer
}

func (e *env) Write(p []byte) (int, error) {
	e.logMu.Lock()
	defer e.logMu.Unlock()
	return e.logBuf.Write(p)
}

func (e *env) log() string { e.logMu.Lock(); defer e.logMu.Unlock(); return e.logBuf.String() }

func newEnvWith(t *testing.T, cfg inspect.Config) *env {
	t.Helper()
	e := &env{t: t, fake: &identify.Fake{}, reg: tunnel.NewRegistry()}
	store, err := hooks.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	e.mgr, _ = hooks.NewManager(context.Background(), store, hooks.SlugConfig{})
	e.hook, err = e.mgr.Create(context.Background(), receiver, hooks.CreateRequest{Path: "cb"})
	if err != nil {
		t.Fatal(err)
	}
	e.in = inspect.New(cfg)
	e.h = &Handler{
		Identifier: e.fake,
		Hooks:      e.mgr,
		Tunnels:    e.reg,
		Audit:      audit.New(slog.New(slog.NewJSONHandler(e, nil))),
		HoldQueue:  2, // small, so TestHoldQueueFull can fill it
		Inspect:    e.in,
	}
	e.srv = httptest.NewServer(e.h)
	t.Cleanup(e.srv.Close)
	return e
}

func newEnv(t *testing.T) *env { return newEnvWith(t, inspect.Config{}) }

// patch updates the hook's settings the way PATCH /api/v1/hooks/{id} does.
func (e *env) patch(p hooks.SettingsPatch) {
	e.t.Helper()
	if _, _, err := e.mgr.Update(context.Background(), receiver, e.hook.ID, hooks.UpdateRequest{Settings: p}); err != nil {
		e.t.Fatal(err)
	}
}

// attach serves the hook through an in-process tunnel to local.
func (e *env) attach(local http.Handler, opts tunnel.Options) (*tunnel.Tunnel, func()) {
	tun, drop := tunneltest.Pair(e.t, local, opts, nil)
	e.reg.Attach(tun, []string{string(e.hook.ID)})
	return tun, drop
}

func (e *env) do(id authz.Identity, method, path string, body io.Reader, hdr http.Header) *http.Response {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, body)
	for k, v := range hdr {
		req.Header[k] = v
	}
	resp, err := e.fake.Client(id).Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func wantProblem(t *testing.T, resp *http.Response, status int, code string) {
	t.Helper()
	b, _ := io.ReadAll(resp.Body)
	var p problem.Problem
	_ = json.Unmarshal(b, &p)
	if resp.StatusCode != status || resp.Header.Get(problem.HeaderError) != code ||
		p.Status != status || p.Type != "https://tsrelay.mazetti.me/errors/"+code {
		t.Fatalf("got %d %q (%s), want %d %q", resp.StatusCode, resp.Header.Get(problem.HeaderError), b, status, code)
	}
}

func TestForwardsBodiesByteForByte(t *testing.T) {
	e := newEnv(t)
	payload := make([]byte, 1<<20)
	rand.Read(payload)
	want := sha256.Sum256(payload)
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if sha256.Sum256(b) != want {
			t.Error("request body changed in transit")
		}
		w.Header().Set("X-Seen", r.Method+" "+r.URL.RequestURI())
		w.WriteHeader(http.StatusAccepted)
		w.Write(b)
	}), tunnel.Options{})

	resp := e.do(sender, "POST", "/~moritz/cb/done?token=s3cr3t", bytes.NewReader(payload), nil)
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 202 || resp.Header.Get("X-Seen") != "POST /done?token=s3cr3t" || sha256.Sum256(got) != want {
		t.Fatalf("got %d %q, body intact=%v", resp.StatusCode, resp.Header.Get("X-Seen"), sha256.Sum256(got) == want)
	}
	if resp.Header.Get(problem.HeaderError) != "" {
		t.Fatal("successful calls must not carry Tsrelay-Error")
	}
}

func TestGzipResponsePassesThrough(t *testing.T) {
	e := newEnv(t)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte(`{"ok":true}`))
	zw.Close()
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(gz.Bytes())
	}), tunnel.Options{})
	resp := e.do(sender, "GET", "/~moritz/cb", nil, nil)
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, gz.Bytes()) || resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatal("gzip response was altered")
	}
}

func TestHeaders(t *testing.T) {
	e := newEnv(t)
	seen := make(chan http.Header, 1)
	hosts := make(chan string, 1)
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
		hosts <- r.Host
	}), tunnel.Options{})
	resp := e.do(sender, "POST", "/~moritz/cb/x", strings.NewReader("{}"), http.Header{
		"Tsrelay-Sender-Node": {"spoofed"},
		"Tsrelay-Tunnel-Hook": {"h_spoofed"},
		"Tsrelay-Anything":    {"spoofed"},
		"X-Forwarded-For":     {"6.6.6.6"},
		"X-Forwarded-Prefix":  {"/spoofed"},
		"X-Forwarded-Uri":     {"/admin"},
		"X-Forwarded-Server":  {"spoofed"},
		"Stripe-Signature":    {"t=1,v1=abc"},
	})
	h := <-seen
	checks := map[string]string{
		"Tsrelay-Sender-Node":  "worker",
		"Tsrelay-Sender-Tags":  "tag:prod-worker",
		"Tsrelay-Sender-Login": "",
		"Tsrelay-Hook":         "moritz/cb",
		"Tsrelay-Tunnel-Hook":  string(e.hook.ID),
		"Tsrelay-Anything":     "",
		"X-Forwarded-For":      "127.0.0.1",
		"X-Forwarded-Host":     strings.TrimPrefix(e.srv.URL, "http://"),
		"X-Forwarded-Proto":    "http",
		"X-Forwarded-Prefix":   "/~moritz/cb",
		"X-Forwarded-Uri":      "",
		"X-Forwarded-Server":   "",
		"Stripe-Signature":     "t=1,v1=abc",
	}
	for k, want := range checks {
		if got := strings.Join(h.Values(k), ","); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if callID := h.Get("Tsrelay-Call-Id"); callID == "" || callID != resp.Header.Get("Tsrelay-Call-Id") {
		t.Errorf("call id local=%q response=%q", callID, resp.Header.Get("Tsrelay-Call-Id"))
	}
	if host := <-hosts; host != strings.TrimPrefix(e.srv.URL, "http://") {
		t.Errorf("Host = %q, want the original host preserved", host)
	}
}

func TestEscapedRemainderIsPreserved(t *testing.T) {
	e := newEnv(t)
	paths := make(chan string, 1)
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { paths <- r.URL.EscapedPath() }), tunnel.Options{})
	e.do(sender, "GET", "/~moritz/cb/a%2Fb/c", nil, nil)
	if got := <-paths; got != "/a%2Fb/c" {
		t.Fatalf("local saw %q, want /a%%2Fb/c", got)
	}
}

func TestStreamsResponses(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "data: 1\n\n")
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, "data: 2\n\n")
	}), tunnel.Options{})
	resp := e.do(sender, "GET", "/~moritz/cb/events", nil, nil)
	buf := make([]byte, 9)
	if _, err := io.ReadFull(resp.Body, buf); err != nil || string(buf) != "data: 1\n\n" {
		t.Fatalf("first event = %q, %v", buf, err)
	}
	close(release)
}

func TestLocalErrorsPassThroughUnmarked(t *testing.T) {
	e := newEnv(t)
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusServiceUnavailable)
	}), tunnel.Options{})
	resp := e.do(sender, "POST", "/~moritz/cb", nil, nil)
	if resp.StatusCode != 503 || resp.Header.Get("Retry-After") != "7" || resp.Header.Get(problem.HeaderError) != "" {
		t.Fatalf("got %d retry=%q err=%q", resp.StatusCode, resp.Header.Get("Retry-After"), resp.Header.Get(problem.HeaderError))
	}
}

// The app's own 502 is not the relay's, even if it claims to be: only the
// CLI forwarder's 564 keeps its Tsrelay-Error.
func TestLocalBadGatewayPassesThroughUnmarked(t *testing.T) {
	e := newEnv(t)
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(problem.HeaderError, problem.UpstreamUnreachable)
		w.WriteHeader(http.StatusBadGateway)
	}), tunnel.Options{})
	resp := e.do(sender, "POST", "/~moritz/cb", nil, nil)
	if resp.StatusCode != 502 || resp.Header.Get(problem.HeaderError) != "" {
		t.Fatalf("got %d err=%q", resp.StatusCode, resp.Header.Get(problem.HeaderError))
	}
}

func TestUpstreamUnreachable(t *testing.T) {
	e := newEnv(t)
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		problem.New(problem.StatusUpstreamUnreachable, problem.UpstreamUnreachable, "the local application is not reachable").Write(w)
	}), tunnel.Options{})
	wantProblem(t, e.do(sender, "POST", "/~moritz/cb", nil, nil), 564, problem.UpstreamUnreachable)
}

func TestErrors(t *testing.T) {
	e := newEnv(t)
	e.patch(hooks.SettingsPatch{HoldTimeout: hooks.Opt[hooks.Duration]{Set: true, Value: new(hooks.Duration(0))}})
	staging := identify.Tagged("w2", []string{"tag:staging"}, `{"roles":["sender"],"channels":["staging"]}`)
	unknown := identify.Tagged("w3", []string{"tag:x"})

	wantProblem(t, e.do(unknown, "POST", "/~moritz/cb", nil, nil), 403, problem.SenderForbidden)
	wantProblem(t, e.do(receiver, "POST", "/~moritz/cb", nil, nil), 403, problem.SenderForbidden)
	wantProblem(t, e.do(sender, "POST", "/~moritz/nope", nil, nil), 404, problem.HookNotFound)
	wantProblem(t, e.do(sender, "POST", "/~nobody/cb", nil, nil), 404, problem.HookNotFound)
	wantProblem(t, e.do(sender, "POST", "/~moritz", nil, nil), 404, problem.HookNotFound)
	// A channel mismatch looks exactly like a missing hook to the sender.
	wantProblem(t, e.do(staging, "POST", "/~moritz/cb", nil, nil), 404, problem.HookNotFound)

	resp := e.do(sender, "POST", "/~moritz/cb", nil, nil)
	wantProblem(t, resp, 561, problem.TunnelUnavailable)
	if resp.Header.Get("Retry-After") == "" {
		t.Error("tunnel_unavailable must carry Retry-After")
	}
}

func TestResponseTimeout(t *testing.T) {
	e := newEnv(t)
	e.patch(hooks.SettingsPatch{ResponseTimeout: hooks.Opt[hooks.Duration]{Set: true, Value: new(hooks.Duration(500 * time.Millisecond))}})
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(5 * time.Second):
		case <-r.Context().Done():
		}
	}), tunnel.Options{})
	wantProblem(t, e.do(sender, "POST", "/~moritz/cb", nil, nil), 565, problem.UpstreamTimeout)
}

func TestOverloaded(t *testing.T) {
	e := newEnv(t)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
	}), tunnel.Options{Concurrency: 1})
	go func() {
		req, _ := http.NewRequest("POST", e.srv.URL+"/~moritz/cb", nil)
		if resp, err := e.fake.Client(sender).Do(req); err == nil {
			resp.Body.Close()
		}
	}()
	<-entered
	resp := e.do(sender, "POST", "/~moritz/cb", nil, nil)
	close(release)
	wantProblem(t, resp, 562, problem.TunnelOverloaded)
}

func TestTunnelLostBeforeHeaders(t *testing.T) {
	e := newEnv(t)
	var drop func()
	entered, done := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(done) })
	// The app never answers: returning when its request is canceled would
	// let the tunnel's server send an empty 200 before the drop arrives.
	_, drop = e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-done
	}), tunnel.Options{})
	go func() { <-entered; drop() }()
	wantProblem(t, e.do(sender, "POST", "/~moritz/cb", nil, nil), 563, problem.TunnelLost)
}

func TestAuditCallRecord(t *testing.T) {
	e := newEnv(t)
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "response-body") }), tunnel.Options{})
	e.do(sender, "POST", "/~moritz/cb/done?token=s3cr3t", strings.NewReader("request-body"),
		http.Header{"Authorization": {"Bearer hunter2"}}).Body.Close()
	e.do(identify.Tagged("w3", []string{"tag:x"}), "POST", "/~moritz/cb", nil, nil).Body.Close()

	// The call record is written after the response completes; wait for both.
	deadline := time.Now().Add(5 * time.Second)
	for strings.Count(e.log(), `"msg":"call"`) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	log := e.log()
	for _, secret := range []string{"s3cr3t", "hunter2", "request-body", "response-body"} {
		if strings.Contains(log, secret) {
			t.Errorf("audit log contains %q:\n%s", secret, log)
		}
	}
	var call map[string]any
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		var rec map[string]any
		json.Unmarshal([]byte(line), &rec)
		if rec["msg"] == audit.Call && rec["status"] == float64(200) {
			call = rec
		}
	}
	if call == nil || call["path"] != "/~moritz/cb/done" || call["method"] != "POST" || call["bytesIn"] != float64(12) || call["bytesOut"] != float64(13) {
		t.Fatalf("call record = %v\nfull log:\n%s", call, log)
	}
	if !strings.Contains(log, `"msg":"authz.deny"`) {
		t.Error("denied sender was not audited")
	}
}

// An encoded slash inside the slug or hook path would route on the decoded
// path but strip segments of the escaped one, delivering a wrong path.
func TestEncodedSlashInPrefixIsNotFound(t *testing.T) {
	e := newEnv(t)
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("local app reached with %s", r.URL.EscapedPath())
	}), tunnel.Options{})
	for _, p := range []string{"/~moritz%2Fcb/x/y", "/~moritz%2fcb"} {
		wantProblem(t, e.do(sender, "GET", p, nil, nil), 404, problem.HookNotFound)
	}
}
