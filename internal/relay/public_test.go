package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

var publicReceiver = identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"],"public":true}`)

// publicEnv is env with e.hook made public and served on a second server
// standing in for the Funnel listener.
type publicEnv struct {
	*env
	pub *httptest.Server
}

func newPublicEnv(t *testing.T) *publicEnv {
	e := newEnv(t)
	h, _, err := e.mgr.Update(context.Background(), publicReceiver, e.hook.ID,
		hooks.UpdateRequest{Public: hooks.Opt[bool]{Set: true, Value: new(true)}})
	if err != nil {
		t.Fatal(err)
	}
	e.hook = h
	pub := httptest.NewServer(http.HandlerFunc(e.h.ServePublic))
	t.Cleanup(pub.Close)
	return &publicEnv{env: e, pub: pub}
}

// attachPublic attaches a tunnel whose owner may receive public calls.
func (e *publicEnv) attachPublic(local http.Handler) *tunnel.Tunnel {
	tun, _ := e.attach(local, tunnel.Options{})
	tun.SetPublic(true)
	return tun
}

func (e *publicEnv) post(path string, hdr http.Header) *http.Response {
	e.t.Helper()
	req, _ := http.NewRequest("POST", e.pub.URL+path, strings.NewReader("{}"))
	for k, v := range hdr {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestPublicCallHeaders(t *testing.T) {
	e := newPublicEnv(t)
	seen := make(chan *http.Request, 1)
	e.attachPublic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen <- r.Clone(r.Context()) }))
	resp := e.post(e.hook.PublicRoute()+"/stripe?x=1", http.Header{
		"Tsrelay-Sender-Node":   {"spoofed"},
		"Tsrelay-Sender-Public": {"false"}, // a spoofed sender identity
		"X-Forwarded-For":       {"6.6.6.6"},
		"X-Forwarded-Server":    {"spoofed"},
		"Forwarded":             {"for=6.6.6.6;proto=http;host=evil.example"},
		"Traceparent":           {"00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"},
		"Tracestate":            {"vendor=spoofed"},
		"Stripe-Signature":      {"t=1,v1=abc"},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	r := <-seen
	if r.URL.RequestURI() != "/stripe?x=1" {
		t.Errorf("forwarded %q, want /stripe?x=1", r.URL.RequestURI())
	}
	checks := map[string]string{
		"Tsrelay-Sender-Public": "true",
		"Tsrelay-Sender-Node":   "",
		"Tsrelay-Sender-Tags":   "",
		"Tsrelay-Sender-Login":  "",
		"Tsrelay-Hook":          "moritz/cb",
		"X-Forwarded-For":       "127.0.0.1",
		"X-Forwarded-Server":    "",
		"X-Forwarded-Prefix":    e.hook.PublicRoute(),
		"Forwarded":             "",
		"Traceparent":           "",
		"Tracestate":            "",
		"Stripe-Signature":      "t=1,v1=abc",
	}
	for k, want := range checks {
		if got := strings.Join(r.Header.Values(k), ","); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	wantHost := strings.TrimPrefix(e.pub.URL, "http://")
	if got := r.Header.Get("X-Forwarded-Host"); got != wantHost {
		t.Errorf("X-Forwarded-Host = %q, want %q", got, wantHost)
	}
	if got := r.Header.Get("X-Forwarded-Proto"); got == "" {
		t.Errorf("X-Forwarded-Proto not set")
	}
	if got := r.Header.Get("Tsrelay-Call-Id"); got == "" {
		t.Errorf("Tsrelay-Call-Id not set")
	}
}

func TestPublicNotFoundIsUniform(t *testing.T) {
	e := newPublicEnv(t)
	tun := e.attachPublic(echo())
	// An unknown token.
	wantProblem(t, e.post("/+AAAAAAAAAAAAAAAAAAAAAA/x", nil), 404, "hook_not_found")
	// The owner lost the public grant.
	tun.SetPublic(false)
	wantProblem(t, e.post(e.hook.PublicRoute()+"/x", nil), 404, "hook_not_found")
	tun.SetPublic(true)
	// The hook was made private again: its old token no longer routes.
	old := e.hook.PublicRoute()
	if _, _, err := e.mgr.Update(context.Background(), publicReceiver, e.hook.ID,
		hooks.UpdateRequest{Public: hooks.Opt[bool]{Set: true, Value: new(false)}}); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, e.post(old+"/x", nil), 404, "hook_not_found")
}

func TestPublicDegeneratePaths(t *testing.T) {
	e := newPublicEnv(t)
	e.attachPublic(echo())
	for _, p := range []string{"/+", "/+/", "/+/x", "/x", "/~moritz/cb"} {
		wantProblem(t, e.post(p, nil), 404, "hook_not_found")
	}
}

func TestPublicEncodedPlusRoutes(t *testing.T) {
	e := newPublicEnv(t)
	e.attachPublic(echo())
	resp := e.post("/%2B"+e.hook.PublicToken+"/x", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d, want 200 for a %%2B-encoded prefix", resp.StatusCode)
	}
}

func TestPublicSkipsSenderAuthorization(t *testing.T) {
	e := newPublicEnv(t)
	e.attachPublic(echo())
	e.fake.Fail(context.Canceled) // no WhoIs may happen for a public call
	if resp := e.post(e.hook.PublicRoute()+"/x", nil); resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestPublicBodyCap(t *testing.T) {
	e := newPublicEnv(t)
	e.h.PublicMaxBody = 4
	e.attachPublic(echo())
	req, _ := http.NewRequest("POST", e.pub.URL+e.hook.PublicRoute()+"/x", strings.NewReader("too long"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	wantProblem(t, resp, 413, "body_too_large")
}

func TestPublicCallRecorded(t *testing.T) {
	e := newPublicEnv(t)
	e.attachPublic(echo())
	c := e.finished(e.post(e.hook.PublicRoute()+"/done", nil))
	if c.Kind != inspect.KindPublic || !c.Sender.Public || c.Sender.IP != "127.0.0.1" || c.Path != "/done" {
		t.Fatalf("call = %+v", c)
	}
}

func TestPublicCallAuditOmitsToken(t *testing.T) {
	e := newPublicEnv(t)
	e.attachPublic(echo())
	e.post(e.hook.PublicRoute()+"/done", nil)
	e.waitLog(`"kind":"public"`)
	log := e.log()
	if strings.Contains(log, e.hook.PublicToken) {
		t.Fatalf("the audit log contains the public token:\n%s", log)
	}
	if !strings.Contains(log, `"path":"/+{token}/done"`) || !strings.Contains(log, `"clientIP":"127.0.0.1"`) {
		t.Fatalf("audit log lacks the redacted path or client IP:\n%s", log)
	}
}

func TestTailnetSenderCannotUsePublicPath(t *testing.T) {
	e := newPublicEnv(t)
	e.attachPublic(echo())
	// On the tailnet handler, /+… is no hook route at all.
	wantProblem(t, e.do(sender, "POST", e.hook.PublicRoute()+"/x", nil, nil), 404, "hook_not_found")
}

// A replayed public call reaches the app as a public call again, under the
// hook's public route, and is recorded as coming from the internet.
func TestPublicReplayHeaders(t *testing.T) {
	e := newPublicEnv(t)
	seen := make(chan http.Header, 1)
	e.attachPublic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen <- r.Header.Clone() }))
	from := httptest.NewRequest("POST", "http://relay.test/api/v1/hooks/x/calls/c/replay", nil)
	c, err := e.h.Inject(context.Background(), Injection{
		Hook: e.hook, Kind: inspect.KindReplay, ReplayOf: "c_orig", Public: true,
		Method: "POST", Path: "/stripe", Header: http.Header{"Traceparent": {"00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"}},
		From: from,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := <-seen
	checks := map[string]string{
		"Tsrelay-Sender-Public": "true",
		"Tsrelay-Sender-Node":   "",
		"X-Forwarded-Prefix":    e.hook.PublicRoute(),
		"Traceparent":           "",
	}
	for k, want := range checks {
		if got := strings.Join(h.Values(k), ","); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if !c.Sender.Public || c.Kind != inspect.KindReplay {
		t.Errorf("call = %+v, want a replay from a public sender", c)
	}
}
