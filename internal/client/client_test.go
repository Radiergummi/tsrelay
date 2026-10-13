package client

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"tailscale.com/ipn/ipnstate"
)

func TestParseTarget(t *testing.T) {
	for in, want := range map[string]string{
		"8080":                       "http://localhost:8080",
		":3000":                      "http://localhost:3000",
		"localhost:9000":             "http://localhost:9000",
		"http://127.0.0.1:8080/hook": "http://127.0.0.1:8080/hook",
		"https://app.test":           "https://app.test",
	} {
		u, err := ParseTarget(in)
		if err != nil || u.String() != want {
			t.Errorf("ParseTarget(%q) = %v, %v; want %s", in, u, err, want)
		}
	}
	for _, bad := range []string{"", "0", "70000", "ftp://x", "http://"} {
		if _, err := ParseTarget(bad); err == nil {
			t.Errorf("ParseTarget(%q) should fail", bad)
		}
	}
}

func TestForwarder(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte("zipped"))
	zw.Close()
	seen := make(chan *http.Request, 1)
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = io.NopCloser(bytes.NewReader(must(io.ReadAll(r.Body))))
		seen <- r
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(gz.Bytes())
	}))
	defer local.Close()
	target, _ := url.Parse(local.URL + "/base")

	req := httptest.NewRequest("POST", "http://tsrelay.example/done?x=1", strings.NewReader("raw-body"))
	req.Host = "tsrelay.example"
	req.Header.Set(tunnel.HeaderHook, "h_1")
	req.Header.Set("X-Forwarded-For", "100.64.0.9")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Prefix", "/~m/cb")
	rec := httptest.NewRecorder()
	NewForwarder(target, Options{}).ServeHTTP(rec, req)

	r := <-seen
	if r.URL.Path != "/base/done" || r.URL.RawQuery != "x=1" || r.Host != "tsrelay.example" {
		t.Fatalf("local saw %s %s host=%s", r.URL.Path, r.URL.RawQuery, r.Host)
	}
	if b, _ := io.ReadAll(r.Body); string(b) != "raw-body" {
		t.Fatalf("body = %q", b)
	}
	if r.Header.Get(tunnel.HeaderHook) != "" {
		t.Fatal("tunnel-internal header leaked to the local app")
	}
	if r.Header.Get("X-Forwarded-For") != "100.64.0.9" || r.Header.Get("X-Forwarded-Proto") != "https" || r.Header.Get("X-Forwarded-Prefix") != "/~m/cb" {
		t.Fatalf("forwarding headers not kept from the relay: %v", r.Header)
	}
	if !bytes.Equal(rec.Body.Bytes(), gz.Bytes()) || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("gzip response altered")
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// Minor 4: the sender learns only that the local app is unreachable; the
// target and the dial error go to the CLI's own log.
func TestForwarderUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	target, _ := url.Parse(srv.URL)
	srv.Close() // nothing listens there any more
	var logged error
	rec := httptest.NewRecorder()
	fwd := NewForwarder(target, Options{OnUpstreamError: func(err error) { logged = err }})
	fwd.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != 502 || rec.Header().Get(problem.HeaderError) != problem.UpstreamUnreachable {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get(problem.HeaderError))
	}
	body := rec.Body.String()
	if !strings.Contains(body, "the local application is not reachable") || strings.Contains(body, target.Host) || strings.Contains(body, "dial") {
		t.Fatalf("problem detail leaks the local target or error: %s", body)
	}
	if logged == nil || !strings.Contains(logged.Error(), target.Host) {
		t.Fatalf("OnUpstreamError got %v, want the full error naming %s", logged, target.Host)
	}
}

func TestForwarderHostRewrite(t *testing.T) {
	hosts := make(chan string, 2)
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(tunnel.HeaderTunnelHost) != "" {
			t.Error("the relay's host marker leaked to the local app")
		}
		hosts <- r.Host
	}))
	defer local.Close()
	target, _ := url.Parse(local.URL)
	fwd := NewForwarder(target, Options{})
	for _, marker := range []string{"", "rewrite"} {
		req := httptest.NewRequest("GET", "http://tsrelay.example/x", nil)
		if marker != "" {
			req.Header.Set(tunnel.HeaderTunnelHost, marker)
		}
		fwd.ServeHTTP(httptest.NewRecorder(), req)
	}
	if got := <-hosts; got != "tsrelay.example" {
		t.Errorf("preserve: local app saw Host %q, want the relay's", got)
	}
	if got := <-hosts; got != target.Host {
		t.Errorf("rewrite: local app saw Host %q, want %q", got, target.Host)
	}
}

func TestForwarderInsecure(t *testing.T) {
	local := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	local.Config.ErrorLog = log.New(io.Discard, "", 0) // the rejected handshake is expected
	local.StartTLS()
	defer local.Close()
	target, _ := url.Parse(local.URL)
	for insecure, want := range map[bool]int{false: http.StatusBadGateway, true: http.StatusTeapot} {
		rec := httptest.NewRecorder()
		NewForwarder(target, Options{Insecure: insecure}).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		if rec.Code != want {
			t.Errorf("Insecure=%v: status %d, want %d", insecure, rec.Code, want)
		}
	}
}

// Minor 5: query strings may carry secrets, so the call log leaves them out.
func TestLogCalls(t *testing.T) {
	var got Call
	h := LogCalls(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) }), func(c Call) { got = c })
	req := httptest.NewRequest("POST", "/done?token=s3cret", nil)
	req.Header.Set("Tsrelay-Sender-Tags", "tag:prod-worker")
	req.Header.Set("Tsrelay-Sender-Node", "worker")
	req.Header.Set(tunnel.HeaderHook, "h_1")
	req.Header.Set("Tsrelay-Call-Id", "c_1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got.Hook != "h_1" || got.Method != "POST" || got.Path != "/done" || got.Status != 201 || got.Sender != "tag:prod-worker" || got.CallID != "c_1" {
		t.Fatalf("call = %+v", got)
	}
}

func TestAPIClientDecodesProblems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		problem.New(422, problem.Invalid, "bad channel").Write(w)
	}))
	defer srv.Close()
	c := &API{BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := c.CreateHook(context.Background(), hooks.CreateRequest{Channel: "billing"})
	var p *problem.Problem
	if !errors.As(err, &p) || p.Code != problem.Invalid {
		t.Fatalf("err = %v", err)
	}
	if got := c.TunnelURL(tunnel.Attach{Session: "s_1", Resume: true}, "h_1", "h_2"); got != srv.URL+"/api/v1/tunnel?hook=h_1&hook=h_2&resume=1&session=s_1" {
		t.Fatalf("TunnelURL = %q", got)
	}
	if got := c.TunnelURL(tunnel.Attach{Session: "s_1", Target: "http://localhost:8080"}, "h_1"); got != srv.URL+"/api/v1/tunnel?hook=h_1&session=s_1&target=http%3A%2F%2Flocalhost%3A8080" {
		t.Fatalf("TunnelURL with target = %q", got)
	}
}

func TestRelayURL(t *testing.T) {
	status := func(context.Context) (*ipnstate.Status, error) {
		return &ipnstate.Status{CurrentTailnet: &ipnstate.TailnetStatus{MagicDNSSuffix: "example.ts.net"}}, nil
	}
	ctx := context.Background()
	for explicit, want := range map[string]string{
		"":                         "https://tsrelay.example.ts.net",
		"relay.example.ts.net":     "https://relay.example.ts.net",
		"http://100.64.0.1/":       "http://100.64.0.1",
		"https://r.example.ts.net": "https://r.example.ts.net",
	} {
		got, err := RelayURL(ctx, explicit, status)
		if err != nil || got != want {
			t.Errorf("RelayURL(%q) = %q, %v; want %q", explicit, got, err, want)
		}
	}
	noDNS := func(context.Context) (*ipnstate.Status, error) { return &ipnstate.Status{}, nil }
	if _, err := RelayURL(ctx, "", noDNS); err == nil || !strings.Contains(err.Error(), "--relay") {
		t.Fatalf("missing MagicDNS should suggest --relay, got %v", err)
	}
}

func TestState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "hooks.json")
	s, err := LoadState(path, "")
	if err != nil || s.Path("k") != "" || s.URL("k") != "" {
		t.Fatalf("empty state: %v %q %q", err, s.Path("k"), s.URL("k"))
	}
	if err := s.Remember("k", "amber-otter-lamp", "https://r/~m/amber-otter-lamp"); err != nil {
		t.Fatal(err)
	}
	s2, _ := LoadState(path, "")
	if s2.Path("k") != "amber-otter-lamp" || s2.URL("k") != "https://r/~m/amber-otter-lamp" {
		t.Fatalf("reloaded = %q %q", s2.Path("k"), s2.URL("k"))
	}
	u, _ := url.Parse("http://localhost:8080")
	if StateKey("https://r", "/proj", u) == StateKey("https://r", "/other", u) {
		t.Fatal("keys must differ per directory")
	}
}

// Minor 10: the state lives where DESIGN.md §15 says.
func TestDefaultStatePath(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)
	if p, err := DefaultStatePath(); err != nil || p != filepath.Join(xdg, "tsrelay", "hooks.json") {
		t.Errorf("with XDG_STATE_HOME: %q, %v", p, err)
	}
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	if p, err := DefaultStatePath(); err != nil || p != filepath.Join(home, ".local", "state", "tsrelay", "hooks.json") {
		t.Errorf("without XDG_STATE_HOME: %q, %v", p, err)
	}
}

// Minor 10: the old state file is read once and moved on the next write.
func TestStateMigratesLegacyFile(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "config", "state.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`{"paths":{"k":"old-path"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state", "hooks.json")
	s, err := LoadState(path, legacy)
	if err != nil || s.Path("k") != "old-path" {
		t.Fatalf("legacy state not read: %v %q", err, s.Path("k"))
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("loading must not write the new file: %v", err)
	}
	if err := s.Remember("k2", "p2", "u2"); err != nil {
		t.Fatal(err)
	}
	moved, _ := LoadState(path, "")
	if moved.Path("k") != "old-path" || moved.Path("k2") != "p2" {
		t.Fatalf("migrated state = %v", moved.Paths)
	}
	if _, err := os.Stat(legacy); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("legacy file should be gone after the migration: %v", err)
	}
	// Once the new file exists, a legacy file is ignored.
	os.WriteFile(legacy, []byte(`{"paths":{"k":"stale"}}`), 0o600)
	if again, _ := LoadState(path, legacy); again.Path("k") != "old-path" {
		t.Fatalf("legacy file won over the new one: %q", again.Path("k"))
	}
}

// Minor 11: the temporary file has a unique name, so a stray or concurrent
// one cannot break a save.
func TestStateTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	if err := os.Mkdir(path+".tmp", 0o700); err != nil { // where a fixed name would write
		t.Fatal(err)
	}
	s, _ := LoadState(path, "")
	if err := s.Remember("k", "p", "u"); err != nil {
		t.Fatalf("save next to a stray %s.tmp: %v", path, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Fatalf("leftover temporary files: %v", entries)
	}
}

func TestStateTargets(t *testing.T) {
	s, _ := LoadState(filepath.Join(t.TempDir(), "hooks.json"), "")
	a, _ := url.Parse("http://localhost:8080")
	b, _ := url.Parse("https://app.test/x")
	s.Remember(StateKey("https://r", "/proj", b), "p2", "u2")
	s.Remember(StateKey("https://r", "/proj", a), "p1", "u1")
	s.Remember(StateKey("https://r", "/proj two", a), "p3", "u3") // another directory
	s.Remember(StateKey("https://other", "/proj", a), "p4", "u4") // another relay
	if got := s.Targets("https://r", "/proj"); !slices.Equal(got, []string{"http://localhost:8080", "https://app.test/x"}) {
		t.Fatalf("Targets = %v", got)
	}
}
