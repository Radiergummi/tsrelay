package release

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeGitHub answers GitHub's latest release API with tag, or fails with
// status, and counts the requests.
type fakeGitHub struct {
	*httptest.Server
	tag    atomic.Value // string
	status atomic.Int32 // 0: 200
	hits   atomic.Int32
}

func newFakeGitHub(t *testing.T, tag string) *fakeGitHub {
	g := &fakeGitHub{}
	g.tag.Store(tag)
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.hits.Add(1)
		if s := g.status.Load(); s != 0 {
			w.WriteHeader(int(s))
			return
		}
		tag := g.tag.Load().(string)
		io.WriteString(w, `{"tag_name":"`+tag+`","html_url":"https://github.com/Radiergummi/tsrelay/releases/tag/`+tag+`"}`)
	}))
	t.Cleanup(g.Close)
	return g
}

func TestCheckCachesTheLatestRelease(t *testing.T) {
	g := newFakeGitHub(t, "v0.4.0")
	c := &Checker{URL: g.URL}
	if got := c.Latest(); got != (Release{}) {
		t.Fatalf("before a check: %+v", got)
	}
	c.Check(t.Context())
	for range 3 {
		if got := c.Latest(); got.Version != "v0.4.0" || got.URL != "https://github.com/Radiergummi/tsrelay/releases/tag/v0.4.0" {
			t.Fatalf("latest = %+v", got)
		}
	}
	if n := g.hits.Load(); n != 1 {
		t.Fatalf("GitHub was asked %d times, want once", n)
	}
}

func TestFailedCheckKeepsTheLastAnswerAndLogsOnce(t *testing.T) {
	g := newFakeGitHub(t, "v0.4.0")
	var logs bytes.Buffer
	c := &Checker{URL: g.URL, Logger: slog.New(slog.NewTextHandler(&logs, nil))}
	c.Check(t.Context())
	g.status.Store(http.StatusForbidden)
	c.Check(t.Context())
	if got := c.Latest(); got.Version != "v0.4.0" {
		t.Fatalf("latest = %+v, want the last good answer", got)
	}
	if n := strings.Count(logs.String(), "level=WARN"); n != 1 || !strings.Contains(logs.String(), "403") {
		t.Fatalf("want one warning naming the status, got:\n%s", logs.String())
	}

	g.status.Store(0)
	g.tag.Store("latest")
	logs.Reset()
	c.Check(t.Context())
	if got := c.Latest(); got.Version != "v0.4.0" || !strings.Contains(logs.String(), "not a version") {
		t.Fatalf("latest = %+v, logs:\n%s", got, logs.String())
	}
}

func TestUnreachableGitHubDoesNotBlock(t *testing.T) {
	var logs bytes.Buffer
	c := &Checker{URL: "http://127.0.0.1:1", Logger: slog.New(slog.NewTextHandler(&logs, nil))}
	done := make(chan struct{})
	go func() { c.Check(t.Context()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Check hung")
	}
	if c.Latest() != (Release{}) || !strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("latest = %+v, logs:\n%s", c.Latest(), logs.String())
	}
}

func TestRunChecksOnStartAndStops(t *testing.T) {
	g := newFakeGitHub(t, "v0.4.0")
	c := &Checker{URL: g.URL}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for c.Latest().Version == "" {
		if time.Now().After(deadline) {
			t.Fatal("Run did not check on start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if n := g.hits.Load(); n != 1 {
		t.Fatalf("GitHub was asked %d times, want once before the first interval", n)
	}
}
