package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/Radiergummi/tsrelay/internal/tunnel/tunneltest"
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
	return e.openTunnelQ(id, "", hookIDs...)
}

// openTunnelQ is openTunnel with extra query parameters such as session and resume.
func (e *env) openTunnelQ(id authz.Identity, extra string, hookIDs ...string) (controls chan tunnel.Control, served chan struct{}, err error) {
	q := extra
	for _, h := range hookIDs {
		if q != "" {
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

// A redial (resume=1) may only displace a tunnel of its own session. A CLI
// that was replaced but missed the "replaced" message must not steal the
// hook back; a fresh start (no resume) still takes over.
func TestResumeCannotTakeOverOtherSessions(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/api/v1/hooks", hooks.CreateRequest{Path: "cb"}, &h)
	if _, _, err := e.openTunnelQ(moritz, "session=s_old", h.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.openTunnelQ(moritz, "session=s_new", h.ID); err != nil {
		t.Fatalf("fresh attach must take over: %v", err)
	}
	_, _, err := e.openTunnelQ(moritz, "session=s_old&resume=1", h.ID)
	var p *problem.Problem
	if !errors.As(err, &p) || p.Status != 409 || p.Code != problem.Conflict {
		t.Fatalf("replaced session resuming: err = %v, want 409 conflict", err)
	}
	var got Hook
	e.call(moritz, "GET", "/api/v1/hooks/"+h.ID, nil, &got)
	if _, _, err := e.openTunnelQ(moritz, "session=s_new&resume=1", h.ID); err != nil {
		t.Fatalf("resuming the serving session (stale half-open tunnel) must work: %v", err)
	}
}

func TestPatchHook(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/api/v1/hooks", map[string]any{"path": "cb", "settings": map[string]any{"holdTimeout": "5s"}}, &h)
	if h.Effective.HoldTimeout != hooks.Duration(5*time.Second) || h.Settings.HoldTimeout == nil {
		t.Fatalf("created = %+v", h)
	}

	var got Hook
	resp := e.call(moritz, "PATCH", "/api/v1/hooks/"+h.ID, map[string]any{"description": "pay", "settings": map[string]any{"holdTimeout": nil}}, &got)
	if resp.StatusCode != 200 || got.Description != "pay" || got.Settings.HoldTimeout != nil || got.Effective.HoldTimeout != hooks.DefaultPolicy.Defaults.HoldTimeout {
		t.Fatalf("patch = %d %+v", resp.StatusCode, got)
	}

	var p problem.Problem
	if resp := e.call(moritz, "PATCH", "/api/v1/hooks/"+h.ID, map[string]any{"channel": "billing"}, &p); resp.StatusCode != 422 {
		t.Fatalf("invalid patch = %d", resp.StatusCode)
	}
	if resp := e.call(moritz, "PATCH", "/api/v1/hooks/"+h.ID, map[string]any{"bogus": 1}, nil); resp.StatusCode != 400 {
		t.Fatalf("unknown field = %d", resp.StatusCode)
	}
	if resp := e.call(other, "PATCH", "/api/v1/hooks/"+h.ID, map[string]any{"description": "x"}, nil); resp.StatusCode != 404 {
		t.Fatalf("other user patch = %d", resp.StatusCode)
	}
	if resp := e.call(admin, "PATCH", "/api/v1/hooks/"+h.ID, map[string]any{"description": "x"}, nil); resp.StatusCode != 404 {
		t.Fatalf("admins may delete but not edit others' hooks: %d", resp.StatusCode)
	}
}

// Deleting a hook fails the calls held for it at once (tunnel.ErrHookGone).
func TestDeleteReleasesHeldCalls(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/api/v1/hooks", map[string]any{"path": "held"}, &h)
	waited := make(chan error, 1)
	go func() {
		_, err := e.reg.Wait(context.Background(), h.ID)
		waited <- err
	}()
	time.Sleep(20 * time.Millisecond) // let Wait block
	if resp := e.call(moritz, "DELETE", "/api/v1/hooks/"+h.ID, nil, nil); resp.StatusCode != 204 {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	select {
	case err := <-waited:
		if !errors.Is(err, tunnel.ErrHookGone) {
			t.Fatalf("Wait = %v, want ErrHookGone", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the held call was not released")
	}
}

func TestAttachPolicyReject(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/api/v1/hooks", map[string]any{"path": "cb", "settings": map[string]any{"attachPolicy": "reject"}}, &h)
	c1, _, err := e.openTunnelQ(moritz, "session=s_a", h.ID)
	if err != nil {
		t.Fatal(err)
	}
	next(t, c1) // attached: the tunnel is registered

	_, _, err = e.openTunnelQ(moritz, "session=s_b", h.ID)
	var p *problem.Problem
	if !errors.As(err, &p) || p.Status != 409 || p.Code != problem.Conflict || !strings.Contains(p.Detail, `"reject"`) {
		t.Fatalf("second session under reject: err = %v, want 409 conflict naming the policy", err)
	}
	// The serving session may still replace its own stale tunnel.
	if _, _, err := e.openTunnelQ(moritz, "session=s_a&resume=1", h.ID); err != nil {
		t.Fatalf("redial of the serving session: %v", err)
	}
}

func TestMaxTunnels(t *testing.T) {
	e := newEnv(t)
	limited := identify.User("lim", "lim@example.com", 7, `{"roles":["receiver"]}`)
	limited.Grants.Limits.MaxTunnels = 2
	var a, b, c Hook
	e.call(limited, "POST", "/api/v1/hooks", hooks.CreateRequest{Path: "a"}, &a)
	e.call(limited, "POST", "/api/v1/hooks", hooks.CreateRequest{Path: "b"}, &b)
	e.call(limited, "POST", "/api/v1/hooks", hooks.CreateRequest{Path: "c"}, &c)
	for i, h := range []Hook{a, b} {
		controls, _, err := e.openTunnelQ(limited, fmt.Sprintf("session=s%d", i), h.ID)
		if err != nil {
			t.Fatal(err)
		}
		next(t, controls) // attached: the tunnel is registered
	}

	_, _, err := e.openTunnelQ(limited, "session=s2", c.ID)
	var p *problem.Problem
	if !errors.As(err, &p) || p.Status != 403 || p.Code != problem.Forbidden || !strings.Contains(p.Detail, "maxTunnels 2") {
		t.Fatalf("third tunnel: err = %v, want 403 naming the limit", err)
	}
	// Taking over your own tunnel's only hook orphans it, so it does not count.
	if _, _, err := e.openTunnelQ(limited, "session=s2", a.ID); err != nil {
		t.Fatalf("takeover at the limit: %v", err)
	}
	// Other users' tunnels never count.
	var o Hook
	e.call(other, "POST", "/api/v1/hooks", hooks.CreateRequest{Path: "o"}, &o)
	if _, _, err := e.openTunnel(other, o.ID); err != nil {
		t.Fatalf("another user's attach: %v", err)
	}
}

// A hook deleted while its tunnel attaches must not stay in the registry
// (plan 1 minor 8). register runs after the attach checks. Here the hook is
// deleted between the two, so its deletion found no tunnel to detach.
func TestAttachRechecksDeletedHooks(t *testing.T) {
	e := newEnv(t)
	a := &API{Hooks: e.mgr, Tunnels: e.reg, Audit: audit.New(slog.New(slog.NewJSONHandler(io.Discard, nil))), BaseURL: "https://r"}
	var keep, gone Hook
	e.call(moritz, "POST", "/api/v1/hooks", hooks.CreateRequest{Path: "keep"}, &keep)
	e.call(moritz, "POST", "/api/v1/hooks", hooks.CreateRequest{Path: "gone"}, &gone)
	hk, _ := e.mgr.Get(hooks.ID(keep.ID))
	hg, _ := e.mgr.Get(hooks.ID(gone.ID))
	if err := e.mgr.Delete(context.Background(), hg.ID); err != nil {
		t.Fatal(err)
	}

	controls := make(chan tunnel.Control, 4)
	tun, _ := tunneltest.Pair(t, http.NotFoundHandler(), tunnel.Options{}, func(c tunnel.Control) { controls <- c })
	a.register(context.Background(), tun, moritz, []hooks.Hook{hk, hg})
	if _, ok := e.reg.Lookup(gone.ID); ok {
		t.Fatal("the deleted hook stayed attached")
	}
	if cur, ok := e.reg.Lookup(keep.ID); !ok || cur != tun {
		t.Fatal("the remaining hook was not attached")
	}
	got := map[tunnel.ControlType][]tunnel.HookInfo{}
	for range 2 {
		c := next(t, controls)
		got[c.Type] = c.Hooks
	}
	if hs := got[tunnel.ControlAttached]; len(hs) != 1 || hs[0].ID != keep.ID {
		t.Errorf("attached = %+v, want only the remaining hook", hs)
	}
	if hs := got[tunnel.ControlHookDeleted]; len(hs) != 1 || hs[0].ID != gone.ID {
		t.Errorf("hook-deleted = %+v, want the deleted hook", hs)
	}

	// A tunnel left with no hooks at all is closed.
	tun2, _ := tunneltest.Pair(t, http.NotFoundHandler(), tunnel.Options{}, nil)
	a.register(context.Background(), tun2, moritz, []hooks.Hook{hg})
	select {
	case <-tun2.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a tunnel whose only hook was deleted was not closed")
	}
	if _, ok := e.reg.Lookup(gone.ID); ok {
		t.Fatal("the deleted hook stayed attached")
	}
}
