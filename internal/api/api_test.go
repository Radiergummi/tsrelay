package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

var (
	moritz = identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"],"channels":["default","prod.*"]}`)
	other  = identify.User("desk", "other@example.com", 2, `{"roles":["receiver"]}`)
	admin  = identify.User("admin-box", "admin@example.com", 3, `{"roles":["admin"]}`)
	worker = identify.Tagged("worker", []string{"tag:prod-worker"}, `{"roles":["sender"]}`)
)

type env struct {
	t    *testing.T
	fake *identify.Fake
	mgr  *hooks.Manager
	reg  *tunnel.Registry
	srv  *httptest.Server
}

func newEnv(t *testing.T) *env {
	e := &env{t: t, fake: &identify.Fake{}, reg: tunnel.NewRegistry()}
	store, _ := hooks.OpenJSON(t.TempDir())
	t.Cleanup(func() { store.Close() })
	e.mgr, _ = hooks.NewManager(context.Background(), store, hooks.SlugConfig{})
	a := &API{
		Identifier: e.fake, Hooks: e.mgr, Tunnels: e.reg,
		Audit:   audit.New(slog.New(slog.NewJSONHandler(io.Discard, nil))),
		BaseURL: "https://tsrelay.example.ts.net",
	}
	mux := http.NewServeMux()
	a.Register(mux)
	e.srv = httptest.NewServer(Guard([]string{"127.0.0.1"}, mux))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) call(id authz.Identity, method, path string, in any, out any) *http.Response {
	e.t.Helper()
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, body)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.fake.Client(id).Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode < 300 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			e.t.Fatal(err)
		}
	} else if p, ok := out.(*problem.Problem); ok {
		json.NewDecoder(resp.Body).Decode(p)
	}
	return resp
}

func TestMe(t *testing.T) {
	e := newEnv(t)
	var me Me
	e.call(moritz, "GET", "/api/v1/me", nil, &me)
	if me.Login != "moritz@example.com" || me.Slug != "moritz" || me.Node != "laptop" ||
		len(me.Roles[authz.RoleReceiver]) != 2 || me.Limits.MaxHooks != authz.DefaultLimits.MaxHooks {
		t.Fatalf("me = %+v", me)
	}
	var w Me
	if resp := e.call(worker, "GET", "/api/v1/me", nil, &w); resp.StatusCode != 200 || w.Slug != "" || w.Login != "" {
		t.Fatalf("tagged me = %d %+v", resp.StatusCode, w)
	}
}

func TestHookCRUD(t *testing.T) {
	e := newEnv(t)
	var h Hook
	resp := e.call(moritz, "POST", "/api/v1/hooks", hooks.CreateRequest{Path: "cb", Ephemeral: true}, &h)
	if resp.StatusCode != 201 || h.URL != "https://tsrelay.example.ts.net/~moritz/cb" || !h.Ephemeral || h.Channel != "default" || h.Tunnel != nil {
		t.Fatalf("create = %d %+v", resp.StatusCode, h)
	}
	if loc := resp.Header.Get("Location"); loc != "/api/v1/hooks/"+h.ID {
		t.Fatalf("Location = %q", loc)
	}

	var list []Hook
	e.call(moritz, "GET", "/api/v1/hooks", nil, &list)
	if len(list) != 1 || list[0].ID != h.ID {
		t.Fatalf("list = %+v", list)
	}
	e.call(other, "GET", "/api/v1/hooks", nil, &list)
	if len(list) != 0 {
		t.Fatalf("other user sees %+v", list)
	}
	e.call(other, "GET", "/api/v1/hooks?all=true", nil, &list)
	if len(list) != 0 {
		t.Fatal("all=true must be ignored for non-admins")
	}
	e.call(admin, "GET", "/api/v1/hooks?all=true", nil, &list)
	if len(list) != 1 {
		t.Fatalf("admin list = %+v", list)
	}

	if resp := e.call(other, "GET", "/api/v1/hooks/"+h.ID, nil, nil); resp.StatusCode != 404 {
		t.Fatalf("other user get = %d, want 404", resp.StatusCode)
	}
	if resp := e.call(other, "DELETE", "/api/v1/hooks/"+h.ID, nil, nil); resp.StatusCode != 404 {
		t.Fatalf("other user delete = %d, want 404", resp.StatusCode)
	}
	if resp := e.call(admin, "DELETE", "/api/v1/hooks/"+h.ID, nil, nil); resp.StatusCode != 204 {
		t.Fatalf("admin delete = %d, want 204", resp.StatusCode)
	}
	if resp := e.call(moritz, "GET", "/api/v1/hooks/"+h.ID, nil, nil); resp.StatusCode != 404 {
		t.Fatalf("get after delete = %d", resp.StatusCode)
	}
}

func TestCreateErrors(t *testing.T) {
	e := newEnv(t)
	var p problem.Problem
	resp := e.call(moritz, "POST", "/api/v1/hooks", hooks.CreateRequest{Channel: "billing"}, &p)
	if resp.StatusCode != 422 || p.Code != problem.Invalid || !strings.Contains(p.Detail+toJSON(p.Errors), "channel") {
		t.Fatalf("invalid channel = %d %+v", resp.StatusCode, p)
	}
	resp = e.call(moritz, "POST", "/api/v1/hooks", map[string]any{"path": "x", "bogus": 1}, &p)
	if resp.StatusCode != 400 {
		t.Fatalf("unknown field = %d", resp.StatusCode)
	}
	resp = e.call(worker, "POST", "/api/v1/hooks", hooks.CreateRequest{}, &p)
	if resp.StatusCode != 403 || p.Code != problem.Forbidden {
		t.Fatalf("tagged create = %d %+v", resp.StatusCode, p)
	}
}

func toJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// openTunnel attaches a tunnel for id to the given hooks and serves it in the
// background. controls receives control messages; served is closed when the
// tunnel ends.
func (e *env) openTunnel(id authz.Identity, hookIDs ...string) (controls chan tunnel.Control, served chan struct{}, err error) {
	q := ""
	for i, h := range hookIDs {
		if i > 0 {
			q += "&"
		}
		q += "hook=" + h
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.t.Cleanup(cancel)
	conn, err := tunnel.Dial(ctx, tunnel.NewHTTPClient(e.fake.Dial(id)), e.srv.URL+"/api/v1/tunnel?"+q)
	if err != nil {
		return nil, nil, err
	}
	controls, served = make(chan tunnel.Control, 8), make(chan struct{})
	go func() {
		tunnel.Serve(ctx, conn, http.NotFoundHandler(), func(c tunnel.Control) { controls <- c })
		close(served)
	}()
	return controls, served, nil
}

func next[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
		panic("unreachable")
	}
}

func TestTunnelAttachAndTakeover(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/api/v1/hooks", hooks.CreateRequest{Path: "cb"}, &h)

	c1, served1, err := e.openTunnel(moritz, h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c := next(t, c1); c.Type != tunnel.ControlAttached || c.Hooks[0].URL != h.URL {
		t.Fatalf("first control = %+v", c)
	}
	var got Hook
	e.call(moritz, "GET", "/api/v1/hooks/"+h.ID, nil, &got)
	if got.Tunnel == nil || got.Tunnel.Node != "laptop" {
		t.Fatalf("hook tunnel = %+v", got.Tunnel)
	}

	c2, _, err := e.openTunnel(moritz, h.ID)
	if err != nil {
		t.Fatal(err)
	}
	next(t, c2) // attached
	if c := next(t, c1); c.Type != tunnel.ControlReplaced || c.Hooks[0].ID != h.ID {
		t.Fatalf("old tunnel got %+v, want replaced", c)
	}
	next(t, served1) // the orphaned tunnel is closed

	// Deleting the hook tells the remaining tunnel and closes it.
	e.call(moritz, "DELETE", "/api/v1/hooks/"+h.ID, nil, nil)
	if c := next(t, c2); c.Type != tunnel.ControlHookDeleted {
		t.Fatalf("got %+v, want hook-deleted", c)
	}
}

func TestTunnelRejections(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/api/v1/hooks", hooks.CreateRequest{Path: "cb"}, &h)
	cases := map[string]struct {
		id     authz.Identity
		hooks  []string
		status int
	}{
		"someone else's hook": {other, []string{h.ID}, 404},
		"unknown hook":        {moritz, []string{"h_nope"}, 404},
		"no hooks":            {moritz, nil, 400},
		"tagged node":         {worker, []string{h.ID}, 403},
	}
	for name, c := range cases {
		_, _, err := e.openTunnel(c.id, c.hooks...)
		var p *problem.Problem
		if !errors.As(err, &p) || p.Status != c.status {
			t.Errorf("%s: err = %v, want status %d", name, err, c.status)
		}
	}
	if resp := e.call(moritz, "GET", "/api/v1/tunnel?hook="+h.ID, nil, nil); resp.StatusCode != 400 {
		t.Errorf("non-upgrade request = %d, want 400", resp.StatusCode)
	}
}
