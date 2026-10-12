package relay

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// unsized hides a reader's length, so the client sends it chunked.
type unsized struct{ io.Reader }

// waitLog waits until the audit log contains s.
func (e *env) waitLog(s string) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(e.log(), s) {
		if time.Now().After(deadline) {
			e.t.Fatalf("audit log lacks %s:\n%s", s, e.log())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMaxBodyBytes(t *testing.T) {
	e := newEnv(t)
	got := make(chan int, 4)
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err == nil {
			got <- len(b)
		}
	}), tunnel.Options{})
	payload := bytes.Repeat([]byte("x"), 4096)

	// Unlimited by default.
	if resp := e.do(sender, "POST", "/~moritz/cb", bytes.NewReader(payload), nil); resp.StatusCode != 200 || <-got != 4096 {
		t.Fatalf("unlimited: %d", resp.StatusCode)
	}

	// Lowering the limit applies to the next call, without a restart.
	e.patch(hooks.SettingsPatch{MaxBodyBytes: hooks.Opt[int64]{Set: true, Value: ptr(int64(1024))}})
	wantProblem(t, e.do(sender, "POST", "/~moritz/cb", bytes.NewReader(payload), nil), 413, problem.BodyTooLarge)
	select {
	case n := <-got:
		t.Fatalf("an oversized Content-Length reached the local app (%d bytes)", n)
	default:
	}
	// Without a Content-Length, the limit applies while the body streams.
	wantProblem(t, e.do(sender, "POST", "/~moritz/cb", unsized{bytes.NewReader(payload)}, nil), 413, problem.BodyTooLarge)
	// Exactly at the limit is fine, with or without a length.
	if resp := e.do(sender, "POST", "/~moritz/cb", bytes.NewReader(payload[:1024]), nil); resp.StatusCode != 200 || <-got != 1024 {
		t.Fatalf("at the limit: %d", resp.StatusCode)
	}
	if resp := e.do(sender, "POST", "/~moritz/cb", unsized{bytes.NewReader(payload[:1024])}, nil); resp.StatusCode != 200 || <-got != 1024 {
		t.Fatalf("at the limit, chunked: %d", resp.StatusCode)
	}
}

// A sender that stops sending its body mid-way is cut off after idleTimeout.
func TestIdleTimeoutWhileReceiving(t *testing.T) {
	e := newEnv(t)
	e.patch(hooks.SettingsPatch{IdleTimeout: hooks.Opt[hooks.Duration]{Set: true, Value: ptr(hooks.Duration(200 * time.Millisecond))}})
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.ReadAll(r.Body) }), tunnel.Options{})
	pr, pw := io.Pipe()
	defer pw.Close()
	go pw.Write([]byte("partial"))
	start := time.Now()
	wantProblem(t, e.do(sender, "POST", "/~moritz/cb", pr, nil), 504, problem.UpstreamTimeout)
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("idle sender answered after %v", d)
	}
}

// A local app that stops sending its response body is cut off as well.
func TestIdleTimeoutWhileResponding(t *testing.T) {
	e := newEnv(t)
	e.patch(hooks.SettingsPatch{IdleTimeout: hooks.Opt[hooks.Duration]{Set: true, Value: ptr(hooks.Duration(200 * time.Millisecond))}})
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "first")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}), tunnel.Options{})
	resp := e.do(sender, "GET", "/~moritz/cb", nil, nil)
	body, err := io.ReadAll(resp.Body)
	if err == nil || string(body) != "first" {
		t.Fatalf("body = %q, err = %v; want the stream cut after the first chunk", body, err)
	}
	e.waitLog(`"error":"upstream_timeout"`)
}

// Waiting for response headers is governed by responseTimeout, not
// idleTimeout, even after the request body is complete.
func TestIdleTimeoutPausesForResponseHeaders(t *testing.T) {
	e := newEnv(t)
	e.patch(hooks.SettingsPatch{IdleTimeout: hooks.Opt[hooks.Duration]{Set: true, Value: ptr(hooks.Duration(100 * time.Millisecond))}})
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		time.Sleep(400 * time.Millisecond)
		io.WriteString(w, "late")
	}), tunnel.Options{})
	for _, body := range []io.Reader{nil, strings.NewReader("abc")} {
		resp := e.do(sender, "POST", "/~moritz/cb", body, nil)
		if got, _ := io.ReadAll(resp.Body); resp.StatusCode != 200 || string(got) != "late" {
			t.Fatalf("got %d %q", resp.StatusCode, got)
		}
	}
}

func TestHostHeaderMarker(t *testing.T) {
	e := newEnv(t)
	seen := make(chan http.Header, 2)
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen <- r.Header.Clone() }), tunnel.Options{})
	spoof := http.Header{tunnel.HeaderTunnelHost: {"rewrite"}}
	e.do(sender, "GET", "/~moritz/cb", nil, spoof)
	if v := (<-seen).Get(tunnel.HeaderTunnelHost); v != "" {
		t.Fatalf("preserve: %s = %q, a sender must not be able to set it", tunnel.HeaderTunnelHost, v)
	}
	e.patch(hooks.SettingsPatch{HostHeader: hooks.Opt[hooks.HostHeader]{Set: true, Value: ptr(hooks.HostRewrite)}})
	e.do(sender, "GET", "/~moritz/cb", nil, nil)
	if v := (<-seen).Get(tunnel.HeaderTunnelHost); v != "rewrite" {
		t.Fatalf("rewrite: %s = %q", tunnel.HeaderTunnelHost, v)
	}
}

// Senders cannot smuggle relay headers in as trailers, nor claim a client
// address through X-Real-IP or X-Forwarded-Port.
func TestStripsSenderTrailersAndAddressHeaders(t *testing.T) {
	e := newEnv(t)
	type seenReq struct{ hdr, trailer http.Header }
	seen := make(chan seenReq, 1)
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		seen <- seenReq{r.Header.Clone(), r.Trailer.Clone()}
	}), tunnel.Options{})
	req, _ := http.NewRequest("POST", e.srv.URL+"/~moritz/cb", unsized{strings.NewReader("body")})
	req.Header.Set("X-Real-IP", "6.6.6.6")
	req.Header.Set("X-Forwarded-Port", "1")
	req.Trailer = http.Header{"Tsrelay-Sender-Node": {"spoofed"}, "X-Checksum": {"abc"}}
	resp, err := e.fake.Client(sender).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	got := <-seen
	for _, h := range []string{"X-Real-Ip", "X-Forwarded-Port"} {
		if v := got.hdr.Get(h); v != "" {
			t.Errorf("%s = %q reached the local app", h, v)
		}
	}
	if _, ok := got.trailer["Tsrelay-Sender-Node"]; ok {
		t.Error("a Tsrelay- trailer reached the local app")
	}
	if _, ok := got.trailer["X-Checksum"]; !ok {
		t.Errorf("other trailers must still be announced: %v", got.trailer)
	}
}

func TestLocalCallIDIsReplaced(t *testing.T) {
	e := newEnv(t)
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(HeaderCallID, "c_local")
	}), tunnel.Options{})
	resp := e.do(sender, "GET", "/~moritz/cb", nil, nil)
	if ids := resp.Header.Values(HeaderCallID); len(ids) != 1 || ids[0] == "c_local" {
		t.Fatalf("Tsrelay-Call-Id = %q, want only the relay's", ids)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type closeRecorder struct {
	io.Reader
	closed bool
}

func (c *closeRecorder) Close() error { c.closed = true; return nil }

// Headers that arrive after the timeout fired are a timeout too, not a
// response whose body then fails with a cancelled context.
func TestTimeoutFiringAsHeadersArrive(t *testing.T) {
	body := &closeRecorder{Reader: strings.NewReader("late")}
	tr := timeoutTransport{timeout: 10 * time.Millisecond, rt: roundTripFunc(func(*http.Request) (*http.Response, error) {
		time.Sleep(100 * time.Millisecond) // ignores the context, like a response already in flight
		return &http.Response{StatusCode: 200, Body: body}, nil
	})}
	req, _ := http.NewRequest("GET", "http://tunnel/", nil)
	resp, err := tr.RoundTrip(req)
	if !errors.Is(err, errResponseTimeout) || resp != nil {
		t.Fatalf("RoundTrip = %v, %v; want errResponseTimeout", resp, err)
	}
	if !body.closed {
		t.Fatal("the late response body was not closed")
	}
}
