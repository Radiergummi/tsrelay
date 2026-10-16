package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"github.com/Radiergummi/tsrelay/internal/tunnel/tunneltest"
)

var (
	publicMoritz = identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"],"public":true}`)
	publicAdmin  = identify.User("admin-box", "admin@example.com", 3, `{"roles":["admin","receiver"],"public":true}`)
)

const publicBase = "https://tsrelay.example.ts.net"

func TestPublicHookJSON(t *testing.T) {
	e := newEnv(t)
	e.api.PublicBaseURL = publicBase
	var h Hook
	if resp := e.call(publicMoritz, "POST", "/hooks", map[string]any{"path": "cb", "public": true}, &h); resp.StatusCode != 201 {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	if !h.Public || !strings.HasPrefix(h.PublicURL, publicBase+"/+") || len(h.PublicURL) != len(publicBase)+2+22 {
		t.Fatalf("hook = %+v", h)
	}
	var seen Hook
	e.call(publicAdmin, "GET", "/hooks/"+h.ID, nil, &seen)
	if !seen.Public || seen.PublicURL != "" {
		t.Fatalf("admin sees %+v; want public without the URL", seen)
	}
}

func TestPublicURLNeedsFunnel(t *testing.T) {
	e := newEnv(t) // PublicBaseURL empty: the relay runs without --funnel
	var h Hook
	e.call(publicMoritz, "POST", "/hooks", map[string]any{"path": "cb", "public": true}, &h)
	if !h.Public || h.PublicURL != "" {
		t.Fatalf("hook = %+v; want public, without a URL", h)
	}
}

func TestPublicNeedsGrant(t *testing.T) {
	e := newEnv(t)
	var p problem.Problem
	resp := e.call(moritz, "POST", "/hooks", map[string]any{"path": "cb", "public": true}, &p)
	// p.Errors is `any` (problem.Problem carries arbitrary error detail), so
	// decode it into the concrete shape hooks.ValidationError serializes as.
	var errs hooks.ValidationError
	b, _ := json.Marshal(p.Errors)
	_ = json.Unmarshal(b, &errs)
	if resp.StatusCode != 422 || len(errs) != 1 || errs[0].Field != "public" {
		t.Fatalf("got %d %+v", resp.StatusCode, p)
	}
}

func TestRotatePublicURL(t *testing.T) {
	e := newEnv(t)
	e.api.PublicBaseURL = publicBase
	var h, r Hook
	e.call(publicMoritz, "POST", "/hooks", map[string]any{"path": "cb", "public": true}, &h)
	if resp := e.call(publicMoritz, "POST", "/hooks/"+h.ID+"/rotate-public-url", nil, &r); resp.StatusCode != 200 || r.PublicURL == h.PublicURL || r.PublicURL == "" {
		t.Fatalf("rotate: %d %+v", resp.StatusCode, r)
	}
	var plain Hook
	e.call(publicMoritz, "POST", "/hooks", map[string]any{"path": "plain"}, &plain)
	var p problem.Problem
	if resp := e.call(publicMoritz, "POST", "/hooks/"+plain.ID+"/rotate-public-url", nil, &p); resp.StatusCode != 409 || p.Code != problem.Conflict {
		t.Fatalf("rotate private: %d %+v", resp.StatusCode, p)
	}
	if resp := e.call(other, "POST", "/hooks/"+h.ID+"/rotate-public-url", nil, nil); resp.StatusCode != 404 {
		t.Fatalf("rotate another's hook: %d", resp.StatusCode)
	}
}

func TestMePublicAndFunnel(t *testing.T) {
	e := newEnv(t)
	var me Me
	e.call(publicMoritz, "GET", "/", nil, &me)
	if !me.Public || me.Funnel {
		t.Fatalf("me = %+v; want public grant, funnel off", me)
	}
	e.api.PublicBaseURL = publicBase
	e.call(moritz, "GET", "/", nil, &me)
	if me.Public || !me.Funnel {
		t.Fatalf("me = %+v; want no public grant, funnel on", me)
	}
}

func TestTunnelGetsPublicURL(t *testing.T) {
	e := newEnv(t)
	e.api.PublicBaseURL = publicBase
	var h Hook
	e.call(publicMoritz, "POST", "/hooks", map[string]any{"path": "cb", "public": true}, &h)
	controls, _, err := e.openTunnel(publicMoritz, h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c := next(t, controls); c.Type != tunnel.ControlAttached || c.Hooks[0].PublicURL != h.PublicURL {
		t.Fatalf("attached = %+v", c)
	}
	var r Hook
	e.call(publicMoritz, "POST", "/hooks/"+h.ID+"/rotate-public-url", nil, &r)
	if c := next(t, controls); c.Type != tunnel.ControlHookUpdated || c.Hooks[0].PublicURL != r.PublicURL {
		t.Fatalf("after rotation = %+v", c)
	}
}

// Making a hook public picks up a public grant its tunnel's node gained
// since it attached, without waiting for the revalidator.
func TestPublicRefreshesTunnelGrant(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/hooks", hooks.CreateRequest{Path: "cb"}, &h)

	// The tunnel's connection, identified as moritz before the grant.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	conn, err := e.fake.Dial(moritz)(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	tun, _ := tunneltest.Pair(t, http.NotFoundHandler(), tunnel.Options{}, nil)
	tun.Owner, tun.RemoteAddr = moritz, conn.LocalAddr().String()
	e.reg.Attach(tun, []string{h.ID})
	if tun.Public() {
		t.Fatal("the tunnel may receive public calls before the grant")
	}

	e.fake.Update(publicMoritz) // an admin adds "public": true
	if resp := e.call(publicMoritz, "PATCH", "/hooks/"+h.ID, map[string]any{"public": true}, nil); resp.StatusCode != 200 {
		t.Fatalf("update: %d", resp.StatusCode)
	}
	if !tun.Public() {
		t.Fatal("the tunnel still may not receive public calls")
	}
}
