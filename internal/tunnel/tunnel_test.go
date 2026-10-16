package tunnel_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"github.com/Radiergummi/tsrelay/internal/tunnel/tunneltest"
)

func get(t *testing.T, tun *tunnel.Tunnel, method, target string, body io.Reader) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, "http://tunnel"+target, body)
	resp, err := tun.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestRoundTrip(t *testing.T) {
	tun, _ := tunneltest.Pair(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Echo", r.Method+" "+r.URL.RequestURI())
		w.WriteHeader(http.StatusCreated)
		w.Write(b)
	}), tunnel.Options{}, nil)

	resp := get(t, tun, "POST", "/a/b?x=1", strings.NewReader("hello"))
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 201 || resp.Header.Get("X-Echo") != "POST /a/b?x=1" || string(body) != "hello" {
		t.Fatalf("got %d %q %q", resp.StatusCode, resp.Header.Get("X-Echo"), body)
	}
}

func TestStreamingResponse(t *testing.T) {
	release := make(chan struct{})
	tun, _ := tunneltest.Pair(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, "second\n")
	}), tunnel.Options{}, nil)

	resp := get(t, tun, "GET", "/", nil)
	defer resp.Body.Close()
	buf := make([]byte, 6)
	if _, err := io.ReadFull(resp.Body, buf); err != nil || string(buf) != "first\n" {
		t.Fatalf("first chunk = %q, %v (must arrive before the handler finishes)", buf, err)
	}
	close(release)
	rest, _ := io.ReadAll(resp.Body)
	if string(rest) != "second\n" {
		t.Fatalf("rest = %q", rest)
	}
}

func TestBodiesAreNotDecompressed(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte("payload"))
	zw.Close()
	tun, _ := tunneltest.Pair(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != "" {
			t.Errorf("tunnel added Accept-Encoding %q", r.Header.Get("Accept-Encoding"))
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(gz.Bytes())
	}), tunnel.Options{}, nil)

	resp := get(t, tun, "GET", "/", nil)
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, gz.Bytes()) || resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatal("gzip body was altered in transit")
	}
}

func TestConcurrencyLimit(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	tun, _ := tunneltest.Pair(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			entered <- struct{}{}
			<-release
		}
	}), tunnel.Options{Concurrency: 1}, nil)

	first := make(chan *http.Response, 1)
	go func() {
		req, _ := http.NewRequest("GET", "http://tunnel/slow", nil)
		resp, err := tun.RoundTrip(req) // t.Fatal is not allowed off the test goroutine
		if err != nil {
			t.Error(err)
		}
		first <- resp
	}()
	<-entered
	req, _ := http.NewRequest("GET", "http://tunnel/fast", nil)
	if _, err := tun.RoundTrip(req); !errors.Is(err, tunnel.ErrOverloaded) {
		t.Fatalf("second request err = %v, want ErrOverloaded", err)
	}
	close(release)
	resp := <-first
	if resp == nil {
		t.FailNow()
	}
	resp.Body.Close() // releases the slot
	get(t, tun, "GET", "/fast", nil).Body.Close()
}

func TestControlMessages(t *testing.T) {
	got := make(chan tunnel.Control, 1)
	tun, _ := tunneltest.Pair(t, http.NotFoundHandler(), tunnel.Options{}, func(c tunnel.Control) { got <- c })
	want := tunnel.Control{Type: tunnel.ControlAttached, Hooks: []tunnel.HookInfo{{ID: "h_1", URL: "https://r/~m/x"}}}
	if err := tun.Send(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if c := <-got; c.Type != want.Type || len(c.Hooks) != 1 || c.Hooks[0] != want.Hooks[0] {
		t.Fatalf("control = %+v", c)
	}
}

func TestDoneWhenClientDrops(t *testing.T) {
	tun, drop := tunneltest.Pair(t, http.NotFoundHandler(), tunnel.Options{}, nil)
	drop()
	select {
	case <-tun.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done not closed after the client dropped")
	}
}

func TestCloseReason(t *testing.T) {
	for _, tc := range []struct {
		name  string
		close func(*tunnel.Tunnel)
		want  string
	}{
		{"default", func(tun *tunnel.Tunnel) { tun.Close() }, "disconnected"},
		{"revoked", func(tun *tunnel.Tunnel) { tun.Notify(tunnel.Control{Type: tunnel.ControlRevoked}, true) }, "revoked"},
		{"orphaned", func(tun *tunnel.Tunnel) { tun.Notify(tunnel.Control{Type: tunnel.ControlReplaced}, true) }, "orphaned"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tun, _ := tunneltest.Pair(t, http.NotFoundHandler(), tunnel.Options{}, nil)
			tc.close(tun)
			select {
			case <-tun.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("tunnel did not close")
			}
			if got := tun.CloseReason(); got != tc.want {
				t.Fatalf("CloseReason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDialReturnsProblem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		problem.New(http.StatusForbidden, problem.Forbidden, "no receiver role").Write(w)
	}))
	defer srv.Close()
	_, err := tunnel.Dial(context.Background(), tunnel.NewHTTPClient(nil), srv.URL)
	var p *problem.Problem
	if !errors.As(err, &p) || p.Status != 403 {
		t.Fatalf("Dial err = %v, want 403 problem", err)
	}
}

// The relay's semaphore is the only concurrency limit (plan 1 minor 6): a
// relay configured above the default must not be capped by the CLI's
// stream limit, and control messages still get through while every slot
// is busy.
func TestConcurrencyAboveDefault(t *testing.T) {
	const n = 40
	entered := make(chan struct{}, n)
	release := make(chan struct{})
	stop := sync.OnceFunc(func() { close(release) })
	tun, _ := tunneltest.Pair(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
	}), tunnel.Options{Concurrency: n}, nil)
	t.Cleanup(stop) // runs before Pair's cleanup, so handlers return first

	errs := make(chan error, n)
	for range n {
		go func() {
			req, _ := http.NewRequest("GET", "http://tunnel/slow", nil)
			resp, err := tun.RoundTrip(req)
			if err == nil {
				resp.Body.Close()
			}
			errs <- err
		}()
	}
	for in := 0; in < n; {
		select {
		case <-entered:
			in++
		case err := <-errs:
			t.Fatalf("a call failed with %d of %d in flight: %v", in, n, err)
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d calls reached the CLI", in, n)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tun.Send(ctx, tunnel.Control{Type: tunnel.ControlAttached}); err != nil {
		t.Fatalf("control message while saturated: %v", err)
	}
	stop()
	for range n {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestPublicFlag(t *testing.T) {
	tun, _ := tunneltest.Pair(t, http.NotFoundHandler(), tunnel.Options{}, nil)
	if tun.Public() {
		t.Fatal("a tunnel whose owner has no public grant starts public")
	}
	tun.SetPublic(true)
	if !tun.Public() {
		t.Fatal("SetPublic(true) did not stick")
	}
}
