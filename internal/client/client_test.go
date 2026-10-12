package client

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
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
	NewForwarder(target).ServeHTTP(rec, req)

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

func TestForwarderUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	target, _ := url.Parse(srv.URL)
	srv.Close() // nothing listens there any more
	rec := httptest.NewRecorder()
	NewForwarder(target).ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != 502 || rec.Header().Get(problem.HeaderError) != problem.UpstreamUnreachable {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get(problem.HeaderError))
	}
}

func TestLogCalls(t *testing.T) {
	var got Call
	h := LogCalls(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) }), func(c Call) { got = c })
	req := httptest.NewRequest("POST", "/done", nil)
	req.Header.Set("Tsrelay-Sender-Tags", "tag:prod-worker")
	req.Header.Set("Tsrelay-Sender-Node", "worker")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got.Method != "POST" || got.Path != "/done" || got.Status != 201 || got.Sender != "tag:prod-worker" {
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
	path := filepath.Join(t.TempDir(), "sub", "state.json")
	s, err := LoadState(path)
	if err != nil || s.Path("k") != "" {
		t.Fatalf("empty state: %v %q", err, s.Path("k"))
	}
	if err := s.SetPath("k", "amber-otter-lamp"); err != nil {
		t.Fatal(err)
	}
	s2, _ := LoadState(path)
	if s2.Path("k") != "amber-otter-lamp" {
		t.Fatalf("reloaded = %q", s2.Path("k"))
	}
	u, _ := url.Parse("http://localhost:8080")
	if StateKey("https://r", "/proj", u) == StateKey("https://r", "/other", u) {
		t.Fatal("keys must differ per directory")
	}
}
