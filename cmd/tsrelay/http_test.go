package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/server"
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

type cliEnv struct {
	t         *testing.T
	fake      *identify.Fake
	relayURL  string
	statePath string
	local     *httptest.Server
	calls     chan string
}

func newCLIEnv(t *testing.T) *cliEnv {
	e := &cliEnv{t: t, fake: &identify.Fake{}, statePath: filepath.Join(t.TempDir(), "state.json"), calls: make(chan string, 8)}
	store, _ := hooks.OpenJSON(t.TempDir())
	t.Cleanup(func() { store.Close() })
	mgr, _ := hooks.NewManager(context.Background(), store, hooks.SlugConfig{})
	var h http.Handler
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(w, r) }))
	e.relayURL = "http://" + ts.Listener.Addr().String()
	srv := server.New(server.Config{BaseURL: e.relayURL, AllowedHosts: []string{"127.0.0.1"}},
		e.fake, mgr, audit.New(slog.New(slog.NewJSONHandler(io.Discard, nil))))
	h = srv.Handler()
	ts.Start()
	t.Cleanup(func() { srv.Close(); ts.Close() })
	e.local = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.calls <- r.Method + " " + r.URL.Path
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(e.local.Close)
	return e
}

type cliRun struct {
	stdout, stderr *syncBuf
	cancel         context.CancelFunc
	exit           chan int
}

func (e *cliEnv) start(extra ...string) *cliRun {
	ctx, cancel := context.WithCancel(context.Background())
	r := &cliRun{stdout: &syncBuf{}, stderr: &syncBuf{}, cancel: cancel, exit: make(chan int, 1)}
	args := append([]string{"http", e.local.URL, "--relay", e.relayURL}, extra...)
	d := deps{HTTP: tunnel.NewHTTPClient(e.fake.Dial(receiver)), StatePath: e.statePath}
	go func() { r.exit <- run(ctx, args, r.stdout, r.stderr, d) }()
	e.t.Cleanup(cancel)
	return r
}

// url waits until the CLI is attached and returns the URL it printed.
func (e *cliEnv) url(r *cliRun) string {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(r.stderr.String(), "Forwarding") {
		if time.Now().After(deadline) {
			e.t.Fatalf("CLI did not attach; stderr:\n%s", r.stderr)
		}
		time.Sleep(5 * time.Millisecond)
	}
	return strings.TrimSpace(r.stdout.String())
}

func (e *cliEnv) send(url string) int {
	e.t.Helper()
	resp, err := e.fake.Client(sender).Post(url+"/done", "application/json", strings.NewReader("{}"))
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func waitExit(t *testing.T, r *cliRun) int {
	t.Helper()
	select {
	case code := <-r.exit:
		return code
	case <-time.After(5 * time.Second):
		t.Fatalf("CLI did not exit; stderr:\n%s", r.stderr)
		return -1
	}
}

func TestHTTPForwardsAndLogs(t *testing.T) {
	e := newCLIEnv(t)
	r := e.start()
	url := e.url(r)
	if !strings.HasPrefix(url, e.relayURL+"/~moritz/") || strings.Count(url, "\n") != 0 {
		t.Fatalf("stdout = %q, want exactly the hook URL", url)
	}
	if code := e.send(url); code != http.StatusAccepted {
		t.Fatalf("send = %d", code)
	}
	if got := <-e.calls; got != "POST /done" {
		t.Fatalf("local got %q", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(r.stderr.String(), "POST /done → 202") {
		if time.Now().After(deadline) {
			t.Fatalf("call not logged; stderr:\n%s", r.stderr)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(r.stderr.String(), "tag:prod-worker") {
		t.Errorf("call line should name the sender:\n%s", r.stderr)
	}
	r.cancel()
	if code := waitExit(t, r); code != 0 {
		t.Fatalf("exit after Ctrl-C = %d", code)
	}
}

// Restarting the CLI must print the same URL, so it can live in a .env file.
func TestHTTPReusesURLAcrossRestarts(t *testing.T) {
	e := newCLIEnv(t)
	r1 := e.start()
	first := e.url(r1)
	r1.cancel()
	waitExit(t, r1)

	r2 := e.start()
	if second := e.url(r2); second != first {
		t.Fatalf("URL changed across restarts: %q → %q", first, second)
	}
	if code := e.send(first); code != http.StatusAccepted {
		t.Fatalf("send after restart = %d", code)
	}
}

func TestHTTPExplicitName(t *testing.T) {
	e := newCLIEnv(t)
	r := e.start("--name", "payments/v1")
	if url := e.url(r); url != e.relayURL+"/~moritz/payments/v1" {
		t.Fatalf("url = %q", url)
	}
}

// A second CLI on the same hook takes it over; the first must exit instead
// of reconnecting and stealing it back.
func TestHTTPTakeover(t *testing.T) {
	e := newCLIEnv(t)
	r1 := e.start("--name", "shared")
	url := e.url(r1)
	r2 := e.start("--name", "shared")
	e.url(r2)
	if code := waitExit(t, r1); code != 1 {
		t.Fatalf("replaced CLI exit = %d, want 1", code)
	}
	if !strings.Contains(r1.stderr.String(), "another") {
		t.Errorf("replaced CLI should explain why it stopped:\n%s", r1.stderr)
	}
	if code := e.send(url); code != http.StatusAccepted {
		t.Fatalf("send after takeover = %d", code)
	}
}

func TestHTTPRelayRefusal(t *testing.T) {
	e := newCLIEnv(t)
	r := e.start("--channel", "Not Valid")
	if code := waitExit(t, r); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(r.stderr.String(), "channel") {
		t.Errorf("stderr should carry the relay's reason:\n%s", r.stderr)
	}
}

func TestHTTPUsage(t *testing.T) {
	for _, args := range [][]string{{"http"}, {"http", "ftp://example.com"}, {"http", "8080", "extra"}} {
		if code := run(context.Background(), args, &syncBuf{}, &syncBuf{}, deps{}); code != 2 {
			t.Errorf("run(%q) = %d, want 2", args, code)
		}
	}
}

// Reusing a hook whose channel or persistence differs from the flags would
// silently ignore them; the CLI must say so instead.
func TestHTTPRefusesMismatchedReuse(t *testing.T) {
	e := newCLIEnv(t)
	r1 := e.start("--name", "cb")
	e.url(r1)
	r1.cancel()
	waitExit(t, r1)

	for flag, want := range map[string]string{"--channel=prod": "channel", "--persistent": "ephemeral"} {
		r := e.start("--name", "cb", flag)
		if code := waitExit(t, r); code != 1 {
			t.Fatalf("%s: exit = %d, want 1", flag, code)
		}
		if out := r.stderr.String(); !strings.Contains(out, want) || !strings.Contains(out, "cb") {
			t.Errorf("%s: stderr should explain the mismatch:\n%s", flag, out)
		}
	}
	r := e.start("--name", "cb", "--channel=default")
	e.url(r) // matching flags still reuse the hook
}
