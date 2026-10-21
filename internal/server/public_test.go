package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
)

var publicReceiver = identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"],"public":true}`)

// publicServer serves e.srv.PublicHandler() as the Funnel listener would,
// for requests whose Host is the relay's public name.
func publicServer(t *testing.T, e *env) *httptest.Server {
	t.Helper()
	ps := httptest.NewServer(e.srv.PublicHandler())
	t.Cleanup(ps.Close)
	return ps
}

func publicGet(t *testing.T, ps *httptest.Server, host, path string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", ps.URL+path, strings.NewReader("{}"))
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestPublicHandlerOffByDefault(t *testing.T) {
	if h := newEnv(t, Config{}).srv.PublicHandler(); h != nil {
		t.Fatal("PublicHandler must be nil without PublicBaseURL")
	}
}

func TestPublicHandlerSurface(t *testing.T) {
	e := newEnv(t, Config{PublicBaseURL: "https://tsrelay.example.ts.net"})
	ps := publicServer(t, e)
	for _, p := range []string{"/", "/hooks", "/tunnel", "/static/x.js",
		"/~moritz/cb", "/healthz", "/readyz", "/metrics", "/install.sh", "/hooks/new",
		"/hooks.json", "/.json", "/openapi.json", "/hooks.json/calls", "/sbom.cdx.json"} {
		resp := publicGet(t, ps, "tsrelay.example.ts.net", p)
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 404 || resp.Header.Get("Tsrelay-Error") != "" ||
			resp.Header.Get("Tsrelay-Version") != "" || strings.Contains(string(b), "tsrelay") {
			t.Errorf("%s: %d %v %q; want a plain 404", p, resp.StatusCode, resp.Header, b)
		}
	}
}

// newPublicHook creates a public hook straight in the manager (the env's
// API client dials as receiver, who lacks the public grant), attaches the
// env's CLI to it, and marks the tunnel public as attach would for an owner
// with the grant.
func newPublicHook(t *testing.T, e *env) hooks.Hook {
	t.Helper()
	h, err := e.srv.hooks.Create(context.Background(), publicReceiver, hooks.CreateRequest{Path: "cb", Public: true})
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	t.Cleanup(local.Close)
	t.Cleanup(e.cli(string(h.ID), local.URL))
	e.waitFor(func() bool { _, ok := e.srv.tunnels.Lookup(h.ID); return ok })
	tun, _ := e.srv.tunnels.Lookup(h.ID)
	tun.SetGrants(publicReceiver.Grants)
	return h
}

func TestPublicHandlerDelivers(t *testing.T) {
	e := newEnv(t, Config{PublicBaseURL: "https://tsrelay.example.ts.net"})
	h := newPublicHook(t, e)
	ps := publicServer(t, e)
	for _, p := range []string{"/+" + h.PublicToken + "/x", "/%2B" + h.PublicToken + "/x"} {
		if resp := publicGet(t, ps, "tsrelay.example.ts.net", p); resp.StatusCode >= 300 || resp.Header.Get("Tsrelay-Call-Id") == "" {
			t.Errorf("%s: status %d, call id %q", p, resp.StatusCode, resp.Header.Get("Tsrelay-Call-Id"))
		}
	}
	if resp := publicGet(t, ps, "evil.example.com", "/+"+h.PublicToken+"/x"); resp.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("foreign Host: status %d, want 421", resp.StatusCode)
	}
}

func TestPublicTokenNotServedOnTailnet(t *testing.T) {
	e := newEnv(t, Config{PublicBaseURL: "https://tsrelay.example.ts.net"})
	h := newPublicHook(t, e)
	req, _ := http.NewRequest("POST", e.base+h.PublicRoute()+"/x", strings.NewReader("{}"))
	resp, err := e.fake.Client(sender).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Tsrelay-Call-Id") != "" {
		t.Fatal("the tailnet listener delivered a call to a public token path")
	}
}
