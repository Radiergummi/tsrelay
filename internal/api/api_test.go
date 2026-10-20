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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/lifecycle"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/relay"
	"github.com/Radiergummi/tsrelay/internal/release"
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
	reg  *lifecycle.Lifecycle
	in   *inspect.Inspector
	srv  *httptest.Server
	api  *API
}

func newEnv(t *testing.T) *env {
	e := &env{t: t, fake: &identify.Fake{}}
	store, _ := hooks.OpenJSON(t.TempDir())
	t.Cleanup(func() { store.Close() })
	e.mgr, _ = hooks.NewManager(context.Background(), store, hooks.SlugConfig{})
	e.in = inspect.New(inspect.Config{})
	al := audit.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	// Tunnels are told public URLs as if Funnel were on; the tests that turn
	// it on for the API set e.api.PublicBaseURL to the same base.
	e.reg = lifecycle.New(lifecycle.Config{Hooks: e.mgr, Inspect: e.in, Audit: al, BaseURL: "https://tsrelay.example.ts.net", PublicBaseURL: publicBase, Identify: e.fake.Identify})
	rh := &relay.Handler{Identifier: e.fake, Hooks: e.mgr, Tunnels: e.reg, Audit: al, Inspect: e.in}
	a := &API{
		Identifier: e.fake, Hooks: e.mgr, Tunnels: e.reg, Inspect: e.in,
		Audit: al, Relay: rh,
		BaseURL: "https://tsrelay.example.ts.net",
		CapName: "example.com/cap/relay",
		Version: "v0.3.0",
		HTML: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "the app")
		}),
	}
	e.api = a
	mux := http.NewServeMux()
	a.Register(mux)
	e.api = a
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
	if method != "GET" {
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
	e.call(moritz, "GET", "/", nil, &me)
	if me.Login != "moritz@example.com" || me.Slug != "moritz" || me.Node != "laptop" ||
		len(me.Roles[authz.RoleReceiver]) != 2 || me.Limits.MaxHooks != authz.DefaultLimits.MaxHooks {
		t.Fatalf("me = %+v", me)
	}
	var w Me
	if resp := e.call(worker, "GET", "/", nil, &w); resp.StatusCode != 200 || w.Slug != "" || w.Login != "" {
		t.Fatalf("tagged me = %d %+v", resp.StatusCode, w)
	}
}

func TestMeNamesCapAndPolicy(t *testing.T) {
	e := newEnv(t)
	var me Me
	if resp := e.call(moritz, "GET", "/", nil, &me); resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if me.CapName != "example.com/cap/relay" || me.Version != "v0.3.0" {
		t.Errorf("capName = %q, version = %q", me.CapName, me.Version)
	}
	d, m := me.Policy.Defaults, me.Policy.Max
	if d.HoldTimeout != hooks.Duration(30*time.Second) || d.AttachPolicy != hooks.AttachTakeover || m.HoldTimeout != hooks.Duration(5*time.Minute) {
		t.Errorf("policy = %+v", me.Policy)
	}
	if me.Policy.CallsPerHook != inspect.DefaultCallsPerHook {
		t.Errorf("policy.callsPerHook = %d, want %d", me.Policy.CallsPerHook, inspect.DefaultCallsPerHook)
	}
	if me.Policy.NoBodies {
		t.Error("policy.noBodies set on a relay that keeps bodies")
	}
}

func TestMeSaysWhenTheRelayKeepsNoBodies(t *testing.T) {
	e := newEnv(t)
	e.api.Inspect = inspect.New(inspect.Config{NoBodies: true})
	var me Me
	if resp := e.call(moritz, "GET", "/", nil, &me); resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !me.Policy.NoBodies {
		t.Error("policy.noBodies not set with --capture-bodies=false")
	}
}

func TestMeNamesNewerReleaseToAdmins(t *testing.T) {
	var latest atomic.Value
	latest.Store("v0.4.0")
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := latest.Load().(string)
		fmt.Fprintf(w, `{"tag_name":%q,"html_url":"https://github.com/Radiergummi/tsrelay/releases/tag/%s"}`, v, v)
	}))
	t.Cleanup(github.Close)
	e := newEnv(t)
	var me Me
	e.call(admin, "GET", "/", nil, &me)
	if me.LatestVersion != "" || me.ReleaseURL != "" {
		t.Fatalf("without a release check: %+v", me)
	}

	e.api.Releases = &release.Checker{URL: github.URL}
	e.api.Releases.Check(t.Context())
	me = Me{}
	e.call(admin, "GET", "/", nil, &me)
	if me.LatestVersion != "v0.4.0" || me.ReleaseURL != "https://github.com/Radiergummi/tsrelay/releases/tag/v0.4.0" {
		t.Fatalf("admin: latestVersion %q, releaseURL %q", me.LatestVersion, me.ReleaseURL)
	}
	for _, id := range []authz.Identity{moritz, worker} {
		var m Me
		e.call(id, "GET", "/", nil, &m)
		if m.LatestVersion != "" || m.ReleaseURL != "" {
			t.Errorf("%s was told about the release: %+v", id.NodeName, m)
		}
	}

	for _, v := range []string{"v0.3.0", "v0.2.0"} { // the relay's own version, and an older one
		latest.Store(v)
		e.api.Releases.Check(t.Context())
		me = Me{}
		e.call(admin, "GET", "/", nil, &me)
		if me.LatestVersion != "" || me.ReleaseURL != "" {
			t.Errorf("latest release %s, relay v0.3.0: %+v", v, me)
		}
	}
}

// Every hook response carries the hook's newest call as a summary, which
// is metadata only, so admins listing every hook see nothing more.
func TestHookLastCall(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/hooks", hooks.CreateRequest{Path: "cb"}, &h)
	if h.LastCall != nil {
		t.Fatalf("a new hook has lastCall %+v", h.LastCall)
	}
	e.seedCall("c_1", h.ID, completeBody(`{"secret":true}`))

	var list []Hook
	e.call(moritz, "GET", "/hooks", nil, &list)
	if len(list) != 1 || list[0].LastCall == nil || list[0].LastCall.ID != "c_1" {
		t.Fatalf("list = %+v", list)
	}
	var one Hook
	e.call(moritz, "GET", "/hooks/"+h.ID, nil, &one)
	if one.LastCall == nil || one.LastCall.ID != "c_1" {
		t.Fatalf("hook = %+v", one)
	}
	var raw json.RawMessage
	if resp := e.call(admin, "GET", "/hooks?all=true", nil, &raw); resp.StatusCode != 200 || !strings.Contains(string(raw), `"lastCall"`) {
		t.Fatalf("admin list = %d %s", resp.StatusCode, raw)
	}
	for _, secret := range []string{"s3cr3t", "secret-header", "secret-reply", "c2VjcmV0"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the admin's hook list contains %q", secret)
		}
	}
	if resp := e.call(other, "GET", "/hooks/"+h.ID, nil, nil); resp.StatusCode != 404 {
		t.Errorf("another user's GET hook = %d, want 404", resp.StatusCode)
	}
}

func TestHookCRUD(t *testing.T) {
	e := newEnv(t)
	var h Hook
	resp := e.call(moritz, "POST", "/hooks", hooks.CreateRequest{Path: "cb", Ephemeral: true}, &h)
	if resp.StatusCode != 201 || h.URL != "https://tsrelay.example.ts.net/~moritz/cb" || !h.Ephemeral || h.Channel != "default" || h.Tunnel != nil {
		t.Fatalf("create = %d %+v", resp.StatusCode, h)
	}
	if loc := resp.Header.Get("Location"); loc != "/hooks/"+h.ID {
		t.Fatalf("Location = %q", loc)
	}

	var list []Hook
	e.call(moritz, "GET", "/hooks", nil, &list)
	if len(list) != 1 || list[0].ID != h.ID {
		t.Fatalf("list = %+v", list)
	}
	e.call(other, "GET", "/hooks", nil, &list)
	if len(list) != 0 {
		t.Fatalf("other user sees %+v", list)
	}
	e.call(other, "GET", "/hooks?all=true", nil, &list)
	if len(list) != 0 {
		t.Fatal("all=true must be ignored for non-admins")
	}
	e.call(admin, "GET", "/hooks?all=true", nil, &list)
	if len(list) != 1 {
		t.Fatalf("admin list = %+v", list)
	}

	if resp := e.call(other, "GET", "/hooks/"+h.ID, nil, nil); resp.StatusCode != 404 {
		t.Fatalf("other user get = %d, want 404", resp.StatusCode)
	}
	if resp := e.call(other, "DELETE", "/hooks/"+h.ID, nil, nil); resp.StatusCode != 404 {
		t.Fatalf("other user delete = %d, want 404", resp.StatusCode)
	}
	if resp := e.call(admin, "DELETE", "/hooks/"+h.ID, nil, nil); resp.StatusCode != 204 {
		t.Fatalf("admin delete = %d, want 204", resp.StatusCode)
	}
	if resp := e.call(moritz, "GET", "/hooks/"+h.ID, nil, nil); resp.StatusCode != 404 {
		t.Fatalf("get after delete = %d", resp.StatusCode)
	}
}

func TestCreateErrors(t *testing.T) {
	e := newEnv(t)
	var p problem.Problem
	resp := e.call(moritz, "POST", "/hooks", hooks.CreateRequest{Channel: "billing"}, &p)
	if resp.StatusCode != 422 || p.Code != problem.Invalid || !strings.Contains(p.Detail+toJSON(p.Errors), "channel") {
		t.Fatalf("invalid channel = %d %+v", resp.StatusCode, p)
	}
	resp = e.call(moritz, "POST", "/hooks", map[string]any{"path": "x", "bogus": 1}, &p)
	if resp.StatusCode != 400 {
		t.Fatalf("unknown field = %d", resp.StatusCode)
	}
	resp = e.call(worker, "POST", "/hooks", hooks.CreateRequest{}, &p)
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
	conn, err := tunnel.Dial(ctx, tunnel.NewHTTPClient(e.fake.Dial(id)), e.srv.URL+"/tunnel?"+q)
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

// attach makes tun serve hookID, as the hook's owner would.
func (e *env) attach(owner authz.Identity, tun *tunnel.Tunnel, hookID string) {
	e.t.Helper()
	tun.Owner = owner
	tk, err := e.reg.Admit(context.Background(), owner, []hooks.ID{hooks.ID(hookID)}, "", false)
	if err != nil {
		e.t.Fatal(err)
	}
	tk.Attach(context.Background(), tun, nil)
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
	e.call(moritz, "POST", "/hooks", hooks.CreateRequest{Path: "cb"}, &h)

	c1, served1, err := e.openTunnel(moritz, h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c := next(t, c1); c.Type != tunnel.ControlAttached || c.Hooks[0].URL != h.URL {
		t.Fatalf("first control = %+v", c)
	}
	var got Hook
	e.call(moritz, "GET", "/hooks/"+h.ID, nil, &got)
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
	e.call(moritz, "DELETE", "/hooks/"+h.ID, nil, nil)
	if c := next(t, c2); c.Type != tunnel.ControlHookDeleted {
		t.Fatalf("got %+v, want hook-deleted", c)
	}
}

func TestTunnelRejections(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/hooks", hooks.CreateRequest{Path: "cb"}, &h)
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
	if resp := e.call(moritz, "GET", "/tunnel?hook="+h.ID, nil, nil); resp.StatusCode != 400 {
		t.Errorf("non-upgrade request = %d, want 400", resp.StatusCode)
	}
}

// A redial (resume=1) may only displace a tunnel of its own session. A CLI
// that was replaced but missed the "replaced" message must not steal the
// hook back; a fresh start (no resume) still takes over.
func TestResumeCannotTakeOverOtherSessions(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/hooks", hooks.CreateRequest{Path: "cb"}, &h)
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
	e.call(moritz, "GET", "/hooks/"+h.ID, nil, &got)
	if _, _, err := e.openTunnelQ(moritz, "session=s_new&resume=1", h.ID); err != nil {
		t.Fatalf("resuming the serving session (stale half-open tunnel) must work: %v", err)
	}
}

func TestPatchHook(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/hooks", map[string]any{"path": "cb", "settings": map[string]any{"holdTimeout": "5s"}}, &h)
	if h.Effective.HoldTimeout != hooks.Duration(5*time.Second) || h.Settings.HoldTimeout == nil {
		t.Fatalf("created = %+v", h)
	}

	var got Hook
	resp := e.call(moritz, "PATCH", "/hooks/"+h.ID, map[string]any{"description": "pay", "settings": map[string]any{"holdTimeout": nil}}, &got)
	if resp.StatusCode != 200 || got.Description != "pay" || got.Settings.HoldTimeout != nil || got.Effective.HoldTimeout != hooks.DefaultPolicy.Defaults.HoldTimeout {
		t.Fatalf("patch = %d %+v", resp.StatusCode, got)
	}

	var p problem.Problem
	if resp := e.call(moritz, "PATCH", "/hooks/"+h.ID, map[string]any{"channel": "billing"}, &p); resp.StatusCode != 422 {
		t.Fatalf("invalid patch = %d", resp.StatusCode)
	}
	if resp := e.call(moritz, "PATCH", "/hooks/"+h.ID, map[string]any{"bogus": 1}, nil); resp.StatusCode != 400 {
		t.Fatalf("unknown field = %d", resp.StatusCode)
	}
	if resp := e.call(other, "PATCH", "/hooks/"+h.ID, map[string]any{"description": "x"}, nil); resp.StatusCode != 404 {
		t.Fatalf("other user patch = %d", resp.StatusCode)
	}
	if resp := e.call(admin, "PATCH", "/hooks/"+h.ID, map[string]any{"description": "x"}, nil); resp.StatusCode != 403 {
		t.Fatalf("admins may delete but not edit others' hooks: %d", resp.StatusCode)
	}
}

// DESIGN.md §6.2: the tunnel serving a hook learns its new URL on PATCH.
func TestPatchNotifiesServingTunnel(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/hooks", hooks.CreateRequest{Path: "cb"}, &h)
	controls, _, err := e.openTunnel(moritz, h.ID)
	if err != nil {
		t.Fatal(err)
	}
	next(t, controls) // attached

	var got Hook
	if resp := e.call(moritz, "PATCH", "/hooks/"+h.ID, map[string]any{"path": "cb2"}, &got); resp.StatusCode != 200 {
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

// Deleting a hook fails the calls held for it at once (lifecycle.ErrHookGone).
func TestDeleteReleasesHeldCalls(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/hooks", map[string]any{"path": "held"}, &h)
	waited := make(chan error, 1)
	go func() {
		_, err := e.reg.Wait(context.Background(), hooks.ID(h.ID))
		waited <- err
	}()
	if resp := e.call(moritz, "DELETE", "/hooks/"+h.ID, nil, nil); resp.StatusCode != 204 {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	select {
	case err := <-waited:
		if !errors.Is(err, lifecycle.ErrHookGone) {
			t.Fatalf("Wait = %v, want ErrHookGone", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the held call was not released")
	}
}

func TestAttachPolicyReject(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/hooks", map[string]any{"path": "cb", "settings": map[string]any{"attachPolicy": "reject"}}, &h)
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
	e.call(limited, "POST", "/hooks", hooks.CreateRequest{Path: "a"}, &a)
	e.call(limited, "POST", "/hooks", hooks.CreateRequest{Path: "b"}, &b)
	e.call(limited, "POST", "/hooks", hooks.CreateRequest{Path: "c"}, &c)
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
	e.call(other, "POST", "/hooks", hooks.CreateRequest{Path: "o"}, &o)
	if _, _, err := e.openTunnel(other, o.ID); err != nil {
		t.Fatalf("another user's attach: %v", err)
	}
}

// Concurrent attaches must not both pass maxTunnels (DESIGN.md §5.5).
func TestConcurrentAttachesRespectMaxTunnels(t *testing.T) {
	e := newEnv(t)
	ids := make([]string, 10) // the default maxHooks
	for i := range ids {
		var h Hook
		if resp := e.call(moritz, "POST", "/hooks", hooks.CreateRequest{Path: fmt.Sprintf("h%d", i)}, &h); resp.StatusCode != 201 {
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
	e.call(moritz, "POST", "/hooks", hooks.CreateRequest{Path: "r",
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
	e.call(moritz, "POST", "/hooks", hooks.CreateRequest{Path: "a"}, &h)
	controls, _, err := e.openTunnel(moritz, h.ID)
	if err != nil {
		t.Fatal(err)
	}
	next(t, controls) // attached
	for _, p := range []string{"a1", "a2"} {
		if resp := e.call(moritz, "PATCH", "/hooks/"+h.ID, hooks.UpdateRequest{Path: hooks.Opt[string]{Set: true, Value: new(p)}}, nil); resp.StatusCode != 200 {
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
	resp := e.call(moritz, "POST", "/hooks?dryRun=true", hooks.CreateRequest{Path: "x", Channel: "prod.eu"}, &h)
	if resp.StatusCode != 200 || h.ID != "" || h.Path != "x" || h.URL != "https://tsrelay.example.ts.net/~moritz/x" {
		t.Fatalf("dry-run create = %d %+v", resp.StatusCode, h)
	}
	var list []Hook
	e.call(moritz, "GET", "/hooks", nil, &list)
	if len(list) != 0 {
		t.Fatalf("a dry run saved a hook: %+v", list)
	}
	var p problem.Problem
	if resp := e.call(moritz, "POST", "/hooks?dryRun=true", hooks.CreateRequest{Channel: "billing"}, &p); resp.StatusCode != 422 || !strings.Contains(p.Detail, "billing") {
		t.Fatalf("invalid dry-run create = %d %+v", resp.StatusCode, p)
	}

	var real Hook
	e.call(moritz, "POST", "/hooks", hooks.CreateRequest{Path: "y"}, &real)
	var got Hook
	resp = e.call(moritz, "PATCH", "/hooks/"+real.ID+"?dryRun=true", hooks.UpdateRequest{Path: hooks.Opt[string]{Set: true, Value: new("z")}}, &got)
	if resp.StatusCode != 200 || got.Path != "z" || got.ID != real.ID {
		t.Fatalf("dry-run update = %d %+v", resp.StatusCode, got)
	}
	e.call(moritz, "GET", "/hooks/"+real.ID, nil, &got)
	if got.Path != "y" {
		t.Fatalf("a dry-run PATCH saved: path = %q", got.Path)
	}

	// The slug check runs too: "moritz" is now bound to the first user.
	twin := identify.User("twin-box", "moritz@other.org", 9, `{"roles":["receiver"]}`)
	p = problem.Problem{}
	if resp := e.call(twin, "POST", "/hooks?dryRun=true", hooks.CreateRequest{}, &p); resp.StatusCode != 422 || !strings.Contains(p.Detail, "belongs to another user") {
		t.Fatalf("dry run with a taken slug = %d %+v", resp.StatusCode, p)
	}
}

func TestTunnelTarget(t *testing.T) {
	e := newEnv(t)
	for target, want := range map[string]string{
		"http%3A%2F%2Flocalhost%3A8080%2Fapp":                    "http://localhost:8080/app",
		"javascript%3Aalert(1)":                                  "",
		"http%3A%2F%2Fuser%3Apw%40localhost":                     "",                          // credentials are never shown
		"http%3A%2F%2Flocalhost%3A8080%2Fapp%3Ftoken%3Dx%23frag": "http://localhost:8080/app", // query and fragment are never shown
		"": "",
	} {
		var h Hook
		e.call(moritz, "POST", "/hooks", hooks.CreateRequest{}, &h)
		controls, _, err := e.openTunnelQ(moritz, "target="+target, h.ID)
		if err != nil {
			t.Fatal(err)
		}
		if c := next(t, controls); c.Type != tunnel.ControlAttached {
			t.Fatalf("first control = %+v, want attached", c)
		}
		var got Hook
		e.call(moritz, "GET", "/hooks/"+h.ID, nil, &got)
		if got.Tunnel == nil || got.Tunnel.Target != want {
			t.Errorf("target %q: tunnel = %+v, want target %q", target, got.Tunnel, want)
		}
	}
}

// A tagged node holds no user identity, so even an admin grant shows it no hooks.
func TestTaggedAdminSeesNothing(t *testing.T) {
	e := newEnv(t)
	ci := identify.Tagged("ci", []string{"tag:ci"}, `{"roles":["admin"]}`)
	var h Hook
	e.call(moritz, "POST", "/hooks", hooks.CreateRequest{Path: "cb"}, &h)
	var list []Hook
	if resp := e.call(ci, "GET", "/hooks?all=true", nil, &list); resp.StatusCode != 200 || len(list) != 0 {
		t.Errorf("tagged admin's list = %d %+v, want []", resp.StatusCode, list)
	}
	if resp := e.call(ci, "GET", "/hooks/"+h.ID, nil, nil); resp.StatusCode != 404 {
		t.Errorf("tagged admin's GET = %d, want 404", resp.StatusCode)
	}
	if resp := e.call(ci, "DELETE", "/hooks/"+h.ID, nil, nil); resp.StatusCode != 404 {
		t.Errorf("tagged admin's DELETE = %d, want 404", resp.StatusCode)
	}
}

// An admin grant covers only its channels.
func TestAdminScopedByChannel(t *testing.T) {
	e := newEnv(t)
	prodAdmin := identify.User("ops-box", "ops@example.com", 4, `{"roles":["admin"],"channels":["prod"]}`)
	var prod, dev Hook
	e.call(other, "POST", "/hooks", hooks.CreateRequest{Path: "p", Channel: "prod"}, &prod)
	e.call(other, "POST", "/hooks", hooks.CreateRequest{Path: "d", Channel: "dev"}, &dev)
	var list []Hook
	if e.call(prodAdmin, "GET", "/hooks?all=true", nil, &list); len(list) != 1 || list[0].ID != prod.ID {
		t.Errorf("prod admin's list = %+v, want only the prod hook", list)
	}
	var cs Channels
	if e.call(prodAdmin, "GET", "/channels?all=true", nil, &cs); !slices.Equal(channelNames(cs.Channels), []string{"prod"}) {
		t.Errorf("prod admin's channels = %+v, want only prod", cs)
	}
	if resp := e.call(prodAdmin, "GET", "/hooks/"+dev.ID, nil, nil); resp.StatusCode != 404 {
		t.Errorf("prod admin's GET of a dev hook = %d, want 404", resp.StatusCode)
	}
	if resp := e.call(prodAdmin, "DELETE", "/hooks/"+prod.ID, nil, nil); resp.StatusCode != 204 {
		t.Errorf("prod admin's DELETE of a prod hook = %d, want 204", resp.StatusCode)
	}
}

// An owner who lost the receiver role still sees and deletes their hooks,
// but may no longer act on them.
func TestOwnerActionsNeedReceiver(t *testing.T) {
	e := newEnv(t)
	e.api.PublicBaseURL = publicBase
	var h Hook
	e.call(publicMoritz, "POST", "/hooks", map[string]any{"path": "cb", "public": true}, &h)
	e.seedCall("c_1", h.ID, completeBody(`{}`))
	bare := identify.User("laptop", "moritz@example.com", 1)
	for _, c := range []struct {
		name, method, path string
		body               any
	}{
		{"PATCH", "PATCH", "/hooks/" + h.ID, map[string]any{"description": "x"}},
		{"rotate", "POST", "/hooks/" + h.ID + "/rotate-public-url", nil},
		{"replay", "POST", "/hooks/" + h.ID + "/calls/c_1/replay", nil},
		{"test", "POST", "/hooks/" + h.ID + "/test", TestRequest{}},
	} {
		if resp := e.call(bare, c.method, c.path, c.body, nil); resp.StatusCode != 403 {
			t.Errorf("%s without the receiver role = %d, want 403", c.name, resp.StatusCode)
		}
	}
	if resp := e.call(bare, "DELETE", "/hooks/"+h.ID, nil, nil); resp.StatusCode != 204 {
		t.Errorf("DELETE without the receiver role = %d, want 204", resp.StatusCode)
	}
}

// Admins see other users' hooks but may not act on them: 403, not 404.
func TestAdminOwnerActions403(t *testing.T) {
	e := newEnv(t)
	e.api.PublicBaseURL = publicBase
	var h Hook
	e.call(publicMoritz, "POST", "/hooks", map[string]any{"path": "cb", "public": true}, &h)
	e.seedCall("c_1", h.ID, completeBody(`{}`))
	for _, c := range []struct {
		name, method, path string
		body               any
	}{
		{"PATCH", "PATCH", "/hooks/" + h.ID, map[string]any{"description": "x"}},
		{"PATCH dry run", "PATCH", "/hooks/" + h.ID + "?dryRun=true", map[string]any{"description": "x"}},
		{"rotate", "POST", "/hooks/" + h.ID + "/rotate-public-url", nil},
		{"replay", "POST", "/hooks/" + h.ID + "/calls/c_1/replay", nil},
		{"test", "POST", "/hooks/" + h.ID + "/test", TestRequest{}},
	} {
		if resp := e.call(admin, c.method, c.path, c.body, nil); resp.StatusCode != 403 {
			t.Errorf("admin %s = %d, want 403", c.name, resp.StatusCode)
		}
	}
}
