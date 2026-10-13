package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

var (
	receiver = identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"]}`)
	sender   = identify.Tagged("worker", []string{"tag:prod-worker"}, `{"roles":["sender"]}`)
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type env struct {
	t    *testing.T
	fake *identify.Fake
	base string
	log  *syncBuf
	api  *client.API
	srv  *Server
}

func newEnv(t *testing.T, cfg Config) *env {
	t.Helper()
	e := &env{t: t, fake: &identify.Fake{}, log: &syncBuf{}}
	store, err := hooks.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	mgr, _ := hooks.NewManager(context.Background(), store, hooks.SlugConfig{})

	var h http.Handler
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(w, r) }))
	e.base = "http://" + ts.Listener.Addr().String()
	cfg.BaseURL = e.base
	cfg.AllowedHosts = []string{"127.0.0.1"}
	s := New(cfg, e.fake, mgr, audit.New(slog.New(slog.NewJSONHandler(e.log, nil))))
	h = s.Handler()
	e.srv = s
	ts.Start()
	t.Cleanup(func() { s.Close(); ts.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go s.RunReaper(ctx, 10*time.Millisecond)

	e.api = &client.API{BaseURL: e.base, HTTP: tunnel.NewHTTPClient(e.fake.Dial(receiver))}
	return e
}

// cli runs what `tsrelay http` runs: a reconnecting tunnel client that
// forwards to target. It returns once the tunnel is attached.
func (e *env) cli(hookID string, target string) (stop func()) {
	e.t.Helper()
	u, _ := url.Parse(target)
	connected := make(chan struct{}, 8)
	ctx, cancel := context.WithCancel(context.Background())
	tc := &tunnel.Client{
		Hooks:  []string{hookID},
		Target: u.Redacted(),
		Dial: func(ctx context.Context, hs []string, a tunnel.Attach) (net.Conn, error) {
			return tunnel.Dial(ctx, e.api.HTTP, e.api.TunnelURL(a, hs...))
		},
		Handler: client.NewForwarder(u, client.Options{}),
		OnState: func(c bool, _ error) {
			if c {
				connected <- struct{}{}
			}
		},
		MinBackoff: time.Millisecond,
	}
	done := make(chan struct{})
	go func() { tc.Run(ctx); close(done) }()
	select {
	case <-connected:
	case <-time.After(5 * time.Second):
		e.t.Fatal("tunnel did not connect")
	}
	// The relay registers the tunnel right after the handshake; wait until the
	// API reports it so the first call cannot race the registration.
	e.waitFor(func() bool {
		hs, _ := e.api.ListHooks(context.Background())
		for _, h := range hs {
			if h.ID == hookID && h.Tunnel != nil {
				return true
			}
		}
		return false
	})
	return func() { cancel(); <-done }
}

func (e *env) waitFor(cond func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			e.t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (e *env) send(hook api.Hook, rest string, body []byte, hdr http.Header) *http.Response {
	e.t.Helper()
	req, _ := http.NewRequest("POST", hook.URL+rest, bytes.NewReader(body))
	for k, v := range hdr {
		req.Header[k] = v
	}
	resp, err := e.fake.Client(sender).Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestSignedWebhookEndToEnd(t *testing.T) {
	e := newEnv(t, Config{})
	secret := []byte("whsec_test")
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, secret)
		mac.Write(body)
		if !hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(r.Header.Get("X-Signature"))) {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		io.WriteString(w, "verified "+r.URL.Path+" from "+r.Header.Get("Tsrelay-Sender-Node"))
	}))
	defer local.Close()

	hook, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer e.cli(hook.ID, local.URL)()

	// Unusual whitespace, key order and unicode: any re-encoding breaks the MAC.
	body := []byte("{ \"z\":1,\n  \"a\" : \"grüße\\u00e9\" }\r\n")
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	resp := e.send(hook, "/payments/done", body, http.Header{"X-Signature": {hex.EncodeToString(mac.Sum(nil))}})
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(got) != "verified /payments/done from worker" {
		t.Fatalf("got %d %q", resp.StatusCode, got)
	}
}

func TestLocalAppDown(t *testing.T) {
	e := newEnv(t, Config{})
	local := httptest.NewServer(http.NotFoundHandler())
	target := local.URL
	local.Close()
	hook, _ := e.api.CreateHook(context.Background(), hooks.CreateRequest{})
	defer e.cli(hook.ID, target)()
	resp := e.send(hook, "", nil, nil)
	if resp.StatusCode != 502 || resp.Header.Get(problem.HeaderError) != problem.UpstreamUnreachable {
		t.Fatalf("got %d %q", resp.StatusCode, resp.Header.Get(problem.HeaderError))
	}
}

func TestReattachAfterCLIRestart(t *testing.T) {
	e := newEnv(t, Config{})
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer local.Close()
	hook, _ := e.api.CreateHook(context.Background(), hooks.CreateRequest{Ephemeral: true})
	stop := e.cli(hook.ID, local.URL)
	stop()
	defer e.cli(hook.ID, local.URL)()
	if resp := e.send(hook, "", nil, nil); resp.StatusCode != 204 {
		t.Fatalf("after reattach: %d", resp.StatusCode)
	}
}

func TestEphemeralHookExpires(t *testing.T) {
	e := newEnv(t, Config{EphemeralGrace: 50 * time.Millisecond})
	local := httptest.NewServer(http.NotFoundHandler())
	defer local.Close()
	eph, _ := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "eph", Ephemeral: true})
	per, _ := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "per"})
	e.cli(eph.ID, local.URL)() // connect, then disconnect

	e.waitFor(func() bool {
		hs, _ := e.api.ListHooks(context.Background())
		return len(hs) == 1 && hs[0].ID == per.ID
	})
	if !strings.Contains(e.log.String(), `"msg":"hook.expire"`) {
		t.Fatal("expiry was not audited")
	}
	if resp := e.send(eph, "", nil, nil); resp.Header.Get(problem.HeaderError) != problem.HookNotFound {
		t.Fatalf("expired hook answered %d", resp.StatusCode)
	}
}

func TestRootAndUnknownPaths(t *testing.T) {
	e := newEnv(t, Config{})
	c := e.fake.Client(receiver)
	resp, err := c.Get(e.base + "/")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET / = %v %v", resp, err)
	}
	resp.Body.Close()
	resp, _ = c.Get(e.base + "/nope")
	resp.Body.Close()
	if resp.StatusCode != 404 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("GET /nope = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	resp, _ = c.Get(e.base + "/api/v1/nope")
	resp.Body.Close()
	if resp.StatusCode != 404 || resp.Header.Get(problem.HeaderError) != problem.NotFound {
		t.Fatalf("GET /api/v1/nope = %d", resp.StatusCode)
	}
}

// An expiring hook is removed by the reaper even while a tunnel serves it,
// and the CLI is told so.
func TestPersistentHookExpires(t *testing.T) {
	e := newEnv(t, Config{})
	local := httptest.NewServer(http.NotFoundHandler())
	defer local.Close()
	exp := time.Now().Add(300 * time.Millisecond)
	hook, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "short", ExpiresAt: &exp})
	if err != nil {
		t.Fatal(err)
	}
	defer e.cli(hook.ID, local.URL)()
	e.waitFor(func() bool {
		hs, _ := e.api.ListHooks(context.Background())
		return len(hs) == 0
	})
	e.waitFor(func() bool { return strings.Contains(e.log.String(), `"msg":"hook.expire"`) })
}

// A call held for an ephemeral hook ends with 404 when the reaper removes
// the hook, instead of waiting out holdTimeout.
func TestHeldCallReleasedWhenReaped(t *testing.T) {
	e := newEnv(t, Config{EphemeralGrace: 300 * time.Millisecond})
	hook, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "eph", Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	resp := e.send(hook, "", nil, nil) // never attached: held until reaped
	if resp.StatusCode != 404 || resp.Header.Get(problem.HeaderError) != problem.HookNotFound {
		t.Fatalf("got %d %q", resp.StatusCode, resp.Header.Get(problem.HeaderError))
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("released after %v", took)
	}
}
