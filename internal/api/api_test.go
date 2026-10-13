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
	"sync"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/relay"
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
	in   *inspect.Inspector
	srv  *httptest.Server
}

func newEnv(t *testing.T) *env {
	e := &env{t: t, fake: &identify.Fake{}, reg: tunnel.NewRegistry()}
	store, _ := hooks.OpenJSON(t.TempDir())
	t.Cleanup(func() { store.Close() })
	e.mgr, _ = hooks.NewManager(context.Background(), store, hooks.SlugConfig{})
	e.in = inspect.New(inspect.Config{})
	al := audit.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	rh := &relay.Handler{Identifier: e.fake, Hooks: e.mgr, Tunnels: e.reg, Audit: al, Inspect: e.in}
	a := &API{
		Identifier: e.fake, Hooks: e.mgr, Tunnels: e.reg, Inspect: e.in,
		Audit: al, Relay: rh,
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

// DESIGN.md §6.2: the tunnel serving a hook learns its new URL on PATCH.
func TestPatchNotifiesServingTunnel(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/api/v1/hooks", hooks.CreateRequest{Path: "cb"}, &h)
	controls, _, err := e.openTunnel(moritz, h.ID)
	if err != nil {
		t.Fatal(err)
	}
	next(t, controls) // attached

	var got Hook
	if resp := e.call(moritz, "PATCH", "/api/v1/hooks/"+h.ID, map[string]any{"path": "cb2"}, &got); resp.StatusCode != 200 {
		t.Fatalf("patch = %d", resp.StatusCode)
	}
	c := next(t, controls)
	if c.Type != tunnel.ControlHookUpdated || len(c.Hooks) != 1 || c.Hooks[0].ID != h.ID || c.Hooks[0].URL != got.URL {
		t.Fatalf("control = %+v, want hook-updated with %s", c, got.URL)
	}
	if !strings.HasSuffix(got.URL, "/cb2") {
		t.Fatalf("patched URL = %s", got.URL)
	}
}

// Deleting a hook fails the calls held for it at once (tunnel.ErrHookGone).
func TestDeleteReleasesHeldCalls(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/api/v1/hooks", map[string]any{"path": "held"}, &h)
	waited := make(chan error, 1)
	go func() {
		_, err := e.reg.Wait(context.Background(), h.ID, func() bool {
			_, ok := e.mgr.Get(hooks.ID(h.ID))
			return ok
		})
		waited <- err
	}()
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

// Concurrent attaches must not both pass maxTunnels (DESIGN.md §5.5).
func TestConcurrentAttachesRespectMaxTunnels(t *testing.T) {
	e := newEnv(t)
	ids := make([]string, 10) // the default maxHooks
	for i := range ids {
		var h Hook
		if resp := e.call(moritz, "POST", "/api/v1/hooks", hooks.CreateRequest{Path: fmt.Sprintf("h%d", i)}, &h); resp.StatusCode != 201 {
			t.Fatalf("create %d: %d", i, resp.StatusCode)
		}
		ids[i] = h.ID
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	attached := 0
	for _, id := range ids {
		wg.Go(func() {
			if _, _, err := e.openTunnel(moritz, id); err == nil {
				mu.Lock()
				attached++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if want := authz.DefaultLimits.MaxTunnels; attached != want {
		t.Fatalf("%d concurrent attaches succeeded, want exactly maxTunnels (%d)", attached, want)
	}
}

// Under "reject", two concurrent attaches to one hook from different
// sessions must not both succeed.
func TestConcurrentAttachesRespectReject(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/api/v1/hooks", hooks.CreateRequest{Path: "r",
		Settings: hooks.Settings{AttachPolicy: new(hooks.AttachReject)}}, &h)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, s := range []string{"s_a", "s_b"} {
		wg.Go(func() {
			_, _, err := e.openTunnelQ(moritz, "session="+s, h.ID)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	ok := 0
	for err := range results {
		if err == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("%d attaches succeeded under the reject policy, want 1", ok)
	}
}

// Each hook-updated carries a sequence number, and the higher number always
// carries the newer URL, even though the messages may arrive in any order.
func TestHookUpdatedSequence(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/api/v1/hooks", hooks.CreateRequest{Path: "a"}, &h)
	controls, _, err := e.openTunnel(moritz, h.ID)
	if err != nil {
		t.Fatal(err)
	}
	next(t, controls) // attached
	for _, p := range []string{"a1", "a2"} {
		if resp := e.call(moritz, "PATCH", "/api/v1/hooks/"+h.ID, hooks.UpdateRequest{Path: hooks.Opt[string]{Set: true, Value: new(p)}}, nil); resp.StatusCode != 200 {
			t.Fatalf("PATCH %s: %d", p, resp.StatusCode)
		}
	}
	c1, c2 := next(t, controls), next(t, controls)
	if c1.Seq == 0 || c2.Seq == 0 || c1.Seq == c2.Seq {
		t.Fatalf("sequence numbers %d, %d", c1.Seq, c2.Seq)
	}
	newest := c1
	if c2.Seq > c1.Seq {
		newest = c2
	}
	if !strings.HasSuffix(newest.Hooks[0].URL, "/a2") {
		t.Fatalf("highest sequence carries %s, want the URL ending in /a2", newest.Hooks[0].URL)
	}
}

func TestDryRun(t *testing.T) {
	e := newEnv(t)
	var h Hook
	resp := e.call(moritz, "POST", "/api/v1/hooks?dryRun=true", hooks.CreateRequest{Path: "x", Channel: "prod.eu"}, &h)
	if resp.StatusCode != 200 || h.ID != "" || h.Path != "x" || h.URL != "https://tsrelay.example.ts.net/~moritz/x" {
		t.Fatalf("dry-run create = %d %+v", resp.StatusCode, h)
	}
	var list []Hook
	e.call(moritz, "GET", "/api/v1/hooks", nil, &list)
	if len(list) != 0 {
		t.Fatalf("a dry run saved a hook: %+v", list)
	}
	var p problem.Problem
	if resp := e.call(moritz, "POST", "/api/v1/hooks?dryRun=true", hooks.CreateRequest{Channel: "billing"}, &p); resp.StatusCode != 422 || !strings.Contains(p.Detail, "billing") {
		t.Fatalf("invalid dry-run create = %d %+v", resp.StatusCode, p)
	}

	var real Hook
	e.call(moritz, "POST", "/api/v1/hooks", hooks.CreateRequest{Path: "y"}, &real)
	var got Hook
	resp = e.call(moritz, "PATCH", "/api/v1/hooks/"+real.ID+"?dryRun=true", hooks.UpdateRequest{Path: hooks.Opt[string]{Set: true, Value: new("z")}}, &got)
	if resp.StatusCode != 200 || got.Path != "z" || got.ID != real.ID {
		t.Fatalf("dry-run update = %d %+v", resp.StatusCode, got)
	}
	e.call(moritz, "GET", "/api/v1/hooks/"+real.ID, nil, &got)
	if got.Path != "y" {
		t.Fatalf("a dry-run PATCH saved: path = %q", got.Path)
	}

	// The slug check runs too: "moritz" is now bound to the first user.
	twin := identify.User("twin-box", "moritz@other.org", 9, `{"roles":["receiver"]}`)
	p = problem.Problem{}
	if resp := e.call(twin, "POST", "/api/v1/hooks?dryRun=true", hooks.CreateRequest{}, &p); resp.StatusCode != 422 || !strings.Contains(p.Detail, "belongs to another user") {
		t.Fatalf("dry run with a taken slug = %d %+v", resp.StatusCode, p)
	}
}

func TestTunnelTarget(t *testing.T) {
	e := newEnv(t)
	for target, want := range map[string]string{
		"http%3A%2F%2Flocalhost%3A8080%2Fapp": "http://localhost:8080/app",
		"javascript%3Aalert(1)":               "",
		"http%3A%2F%2Fuser%3Apw%40localhost":  "", // credentials are never shown
		"":                                    "",
	} {
		var h Hook
		e.call(moritz, "POST", "/api/v1/hooks", hooks.CreateRequest{}, &h)
		if _, _, err := e.openTunnelQ(moritz, "target="+target, h.ID); err != nil {
			t.Fatal(err)
		}
		var got Hook
		e.call(moritz, "GET", "/api/v1/hooks/"+h.ID, nil, &got)
		if got.Tunnel == nil || got.Tunnel.Target != want {
			t.Errorf("target %q: tunnel = %+v, want target %q", target, got.Tunnel, want)
		}
	}
}
