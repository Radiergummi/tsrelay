package client_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/server"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

var (
	receiver = identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"]}`)
	sender   = identify.Tagged("worker", []string{"tag:prod-worker"}, `{"roles":["sender"]}`)
)

// sessionEnv is a relay over a fresh hook store, a local app answering 202,
// and an API client for the receiver whose new connections fail while
// offline is set.
type sessionEnv struct {
	t       *testing.T
	fake    *identify.Fake
	mgr     *hooks.Manager
	relay   string
	local   *httptest.Server
	api     *client.API
	offline atomic.Bool
	who     atomic.Pointer[authz.Identity] // what the session's new connections are identified as

	mu  sync.Mutex
	srv *server.Server
	hc  *http.Client
}

func newSessionEnv(t *testing.T) *sessionEnv {
	e := &sessionEnv{t: t, fake: &identify.Fake{}}
	store, _ := hooks.OpenJSON(t.TempDir())
	t.Cleanup(func() { store.Close() })
	e.mgr, _ = hooks.NewManager(context.Background(), store, hooks.SlugConfig{})
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		h := e.srv.Handler()
		e.mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	e.relay = "http://" + ts.Listener.Addr().String()
	e.srv = e.newServer()
	ts.Start()
	t.Cleanup(func() { e.mu.Lock(); e.srv.Close(); e.mu.Unlock(); ts.Close() })
	e.local = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	t.Cleanup(e.local.Close)
	e.who.Store(&receiver)
	e.hc = tunnel.NewHTTPClient(func(ctx context.Context, network, addr string) (net.Conn, error) {
		if e.offline.Load() {
			return nil, errors.New("offline")
		}
		return e.fake.Dial(*e.who.Load())(ctx, network, addr)
	})
	e.api = &client.API{BaseURL: e.relay, HTTP: e.hc}
	return e
}

func (e *sessionEnv) newServer() *server.Server {
	return server.New(server.Config{BaseURL: e.relay, AllowedHosts: []string{"127.0.0.1"}},
		e.fake, e.mgr, audit.New(slog.New(slog.NewJSONHandler(io.Discard, nil))))
}

// setup is an API client for the receiver that works while e is offline.
func (e *sessionEnv) setup() *client.API {
	return &client.API{BaseURL: e.relay, HTTP: tunnel.NewHTTPClient(e.fake.Dial(receiver))}
}

func (e *sessionEnv) create(path string) api.Hook {
	e.t.Helper()
	h, err := e.setup().CreateHook(context.Background(), hooks.CreateRequest{Path: path})
	if err != nil {
		e.t.Fatal(err)
	}
	return h
}

func (e *sessionEnv) delete(h api.Hook) {
	e.t.Helper()
	if err := e.setup().DeleteHook(context.Background(), h.ID); err != nil {
		e.t.Fatal(err)
	}
}

// goOffline drops the session's tunnel and keeps it from redialing until
// goOnline. The relay is replaced by a fresh instance over the same store.
func (e *sessionEnv) goOffline() {
	e.offline.Store(true)
	e.hc.CloseIdleConnections()
	e.mu.Lock()
	defer e.mu.Unlock()
	e.srv.Close()
	e.srv = e.newServer()
}

func (e *sessionEnv) goOnline() { e.offline.Store(false) }

func (e *sessionEnv) send(h api.Hook) int {
	e.t.Helper()
	resp, err := e.fake.Client(sender).Post(h.URL, "application/json", nil)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func (e *sessionEnv) session(hs ...api.Hook) *client.Session {
	target, err := url.Parse(e.local.URL)
	if err != nil {
		e.t.Fatal(err)
	}
	return &client.Session{API: e.api, Hooks: hs, Target: target}
}

// start runs s, collecting its events.
func start(t *testing.T, s *client.Session) (<-chan client.Event, <-chan error) {
	events := make(chan client.Event, 64)
	s.OnEvent = func(ev client.Event) { events <- ev }
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	return events, done
}

// next waits for the next event of kind, skipping others.
func next(t *testing.T, events <-chan client.Event, kind client.EventKind) client.Event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Kind == kind {
				return ev
			}
		case <-timeout:
			t.Fatalf("no event of kind %d", kind)
		}
	}
}

func wait(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
		return nil
	}
}

// drain returns the events left once Run has returned.
func drain(events <-chan client.Event) []client.Event {
	var out []client.Event
	for {
		select {
		case ev := <-events:
			out = append(out, ev)
		default:
			return out
		}
	}
}

// served waits until the relay delivers a call to h over the session's
// tunnel: the tunnel is registered, so closing the relay drops it.
func (e *sessionEnv) served(events <-chan client.Event, h api.Hook) {
	e.t.Helper()
	next(e.t, events, client.EventConnected)
	if code := e.send(h); code != http.StatusAccepted {
		e.t.Fatalf("send = %d", code)
	}
	next(e.t, events, client.EventCall)
}

func ids(hs []api.Hook) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.ID
	}
	return out
}

func TestSessionFollowsHookUpdate(t *testing.T) {
	e := newSessionEnv(t)
	h := e.create("pay")
	s := e.session(h)
	events, _ := start(t, s)
	next(t, events, client.EventConnected)
	moved, err := e.setup().UpdateHook(context.Background(), h.ID, hooks.UpdateRequest{Path: hooks.Opt[string]{Set: true, Value: new("billing")}})
	if err != nil {
		t.Fatal(err)
	}
	ev := next(t, events, client.EventHookChanged)
	if ev.Hook.Path != "billing" || ev.Hook.URL != moved.URL || ev.OldID != h.ID || ev.Recreated {
		t.Fatalf("changed = %+v", ev)
	}
	if cur := s.Current(); len(cur) != 1 || cur[0].Path != "billing" || cur[0].URL != moved.URL {
		t.Fatalf("Current = %+v", cur)
	}
}

// A rename while the tunnel is down reaches the session with the attached
// message of the redial, not only with the hook's next update.
func TestSessionFollowsHookUpdateDuringOutage(t *testing.T) {
	e := newSessionEnv(t)
	h := e.create("pay")
	s := e.session(h)
	events, _ := start(t, s)
	e.served(events, h)
	e.goOffline()
	next(t, events, client.EventLost)
	moved, err := e.setup().UpdateHook(context.Background(), h.ID, hooks.UpdateRequest{Path: hooks.Opt[string]{Set: true, Value: new("billing")}})
	if err != nil {
		t.Fatal(err)
	}
	e.goOnline()
	ev := next(t, events, client.EventHookChanged)
	if ev.Hook.Path != "billing" || ev.Hook.URL != moved.URL || ev.OldID != h.ID || ev.Recreated {
		t.Fatalf("changed = %+v", ev)
	}
	if cur := s.Current(); len(cur) != 1 || cur[0].URL != moved.URL {
		t.Fatalf("Current = %+v", cur)
	}
}

func TestSessionDropsOnControl(t *testing.T) {
	e := newSessionEnv(t)
	pay, ship := e.create("pay"), e.create("ship")
	s := e.session(pay, ship)
	events, done := start(t, s)
	next(t, events, client.EventConnected)
	e.delete(pay)
	ev := next(t, events, client.EventHookDropped)
	if ev.Hook.ID != pay.ID || ev.Hook.URL != pay.URL || ev.Reason != client.DropDeleted {
		t.Fatalf("dropped = %+v", ev)
	}
	if got := ids(s.Current()); !slices.Equal(got, []string{ship.ID}) {
		t.Fatalf("Current = %v", got)
	}
	e.delete(ship)
	if ev := next(t, events, client.EventHookDropped); ev.Hook.ID != ship.ID {
		t.Fatalf("second drop = %+v", ev)
	}
	if err := wait(t, done); !errors.Is(err, tunnel.ErrNoHooks) {
		t.Fatalf("Run = %v, want ErrNoHooks", err)
	}
	for _, ev := range drain(events) {
		if ev.Kind == client.EventHookDropped {
			t.Fatalf("a hook was dropped twice: %+v", ev)
		}
	}
}

func TestSessionDropsRefusedHookOnRedial(t *testing.T) {
	e := newSessionEnv(t)
	pay, ship, inv := e.create("pay"), e.create("ship"), e.create("inv")
	s := e.session(pay, ship, inv)
	events, _ := start(t, s)
	e.served(events, inv)

	e.goOffline()
	next(t, events, client.EventLost)
	e.delete(pay)
	// A fresh attach from another session takes ship over.
	setup := e.setup()
	conn, err := tunnel.Dial(context.Background(), setup.HTTP, setup.TunnelURL(tunnel.Attach{Session: "s_other"}, ship.ID))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	e.goOnline()

	got := map[string]client.DropReason{}
	for range 2 {
		ev := next(t, events, client.EventHookDropped)
		got[ev.Hook.ID] = ev.Reason
	}
	if got[pay.ID] != client.DropDeleted || got[ship.ID] != client.DropReplaced {
		t.Fatalf("drops = %v", got)
	}
	next(t, events, client.EventConnected)
	if cur := ids(s.Current()); !slices.Equal(cur, []string{inv.ID}) {
		t.Fatalf("Current = %v", cur)
	}
	if code := e.send(inv); code != http.StatusAccepted {
		t.Fatalf("send = %d", code)
	}
	if ev := next(t, events, client.EventCall); ev.Call.Status != http.StatusAccepted || ev.Call.Hook != inv.ID {
		t.Fatalf("call = %+v", ev.Call)
	}
}

// Every hook refused in turn: each is reported once, then the session ends.
func TestSessionDropsEveryRefusedHook(t *testing.T) {
	e := newSessionEnv(t)
	pay, ship := e.create("pay"), e.create("ship")
	events, done := start(t, e.session(pay, ship))
	e.served(events, pay)
	e.goOffline()
	next(t, events, client.EventLost)
	e.delete(pay)
	e.delete(ship)
	e.goOnline()
	if err := wait(t, done); !errors.Is(err, tunnel.ErrNoHooks) {
		t.Fatalf("Run = %v, want ErrNoHooks", err)
	}
	dropped := 0
	for _, ev := range drain(events) {
		switch ev.Kind {
		case client.EventHookDropped:
			dropped++
		case client.EventConnected:
			t.Fatal("connected with no hooks left")
		}
	}
	if dropped != 2 {
		t.Fatalf("%d drops, want 2", dropped)
	}
}

func TestSessionRecreatesOnRedial(t *testing.T) {
	e := newSessionEnv(t)
	pay := e.create("pay")
	s := e.session(pay)
	s.Recreate = func(ctx context.Context, gone api.Hook) (api.Hook, error) {
		return e.api.CreateHook(ctx, hooks.CreateRequest{Path: gone.Path})
	}
	events, _ := start(t, s)
	e.served(events, pay)
	e.goOffline()
	next(t, events, client.EventLost)
	e.delete(pay)
	e.goOnline()
	ev := next(t, events, client.EventHookChanged)
	if !ev.Recreated || ev.OldID != pay.ID || ev.Hook.ID == pay.ID || ev.Hook.Path != "pay" {
		t.Fatalf("changed = %+v", ev)
	}
	next(t, events, client.EventConnected)
	if cur := ids(s.Current()); !slices.Equal(cur, []string{ev.Hook.ID}) {
		t.Fatalf("Current = %v, want the new hook", cur)
	}
}

func TestSessionFirstDialRefusal(t *testing.T) {
	e := newSessionEnv(t)
	pay := e.create("pay")
	e.delete(pay)
	events, done := start(t, e.session(pay))
	var p *problem.Problem
	if err := wait(t, done); !errors.As(err, &p) || p.Status != http.StatusNotFound {
		t.Fatalf("Run = %v, want the relay's 404", err)
	}
	for _, ev := range drain(events) {
		if ev.Kind == client.EventConnected || ev.Kind == client.EventHookDropped {
			t.Fatalf("unexpected event %+v", ev)
		}
	}

	// With Recreate, the first dial recreates instead.
	s := e.session(pay)
	s.Recreate = func(ctx context.Context, gone api.Hook) (api.Hook, error) {
		return e.api.CreateHook(ctx, hooks.CreateRequest{Path: gone.Path})
	}
	events, _ = start(t, s)
	ev := next(t, events, client.EventHookChanged)
	if !ev.Recreated || ev.OldID != pay.ID {
		t.Fatalf("changed = %+v", ev)
	}
	next(t, events, client.EventConnected)
	if cur := ids(s.Current()); !slices.Equal(cur, []string{ev.Hook.ID}) {
		t.Fatalf("Current = %v, want the recreated hook", cur)
	}
}

// A recreated hook the relay refuses again ends the session, rather than
// recreating it over and over.
func TestSessionRecreatesOncePerDial(t *testing.T) {
	e := newSessionEnv(t)
	pay := e.create("pay")
	e.delete(pay)
	var calls atomic.Int32
	s := e.session(pay)
	s.Recreate = func(_ context.Context, gone api.Hook) (api.Hook, error) {
		calls.Add(1)
		return gone, nil // still gone
	}
	_, done := start(t, s)
	var p *problem.Problem
	if err := wait(t, done); !errors.As(err, &p) || p.Status != http.StatusNotFound {
		t.Fatalf("Run = %v, want the relay's 404", err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("Recreate called %d times, want 1", n)
	}
}

// Hooks whose channel the receiver may no longer serve are refused one by
// one on the redial, each dropped as revoked.
func TestSessionDropsRevokedHooksOnRedial(t *testing.T) {
	e := newSessionEnv(t)
	pay, ship := e.create("pay"), e.create("ship")
	events, done := start(t, e.session(pay, ship))
	e.served(events, pay)
	e.goOffline()
	next(t, events, client.EventLost)
	other := identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"],"channels":["elsewhere"]}`)
	e.who.Store(&other)
	e.goOnline()
	if err := wait(t, done); !errors.Is(err, tunnel.ErrNoHooks) {
		t.Fatalf("Run = %v, want ErrNoHooks", err)
	}
	reasons := map[string]client.DropReason{}
	for _, ev := range drain(events) {
		if ev.Kind == client.EventHookDropped {
			reasons[ev.Hook.ID] = ev.Reason
		}
	}
	if len(reasons) != 2 || reasons[pay.ID] != client.DropRevoked || reasons[ship.ID] != client.DropRevoked {
		t.Fatalf("drops = %v, want both revoked", reasons)
	}
}

// Losing the receiver role is not about one hook: the redial's refusal
// ends the session, and no hook is dropped.
func TestSessionEndsWhenRoleLost(t *testing.T) {
	e := newSessionEnv(t)
	pay := e.create("pay")
	events, done := start(t, e.session(pay))
	e.served(events, pay)
	e.goOffline()
	next(t, events, client.EventLost)
	nobody := identify.User("laptop", "moritz@example.com", 1, `{"roles":["sender"]}`)
	e.who.Store(&nobody)
	e.goOnline()
	var p *problem.Problem
	if err := wait(t, done); !errors.As(err, &p) || p.Status != http.StatusForbidden || p.Hook != "" {
		t.Fatalf("Run = %v, want the relay's hookless 403", err)
	}
	for _, ev := range drain(events) {
		if ev.Kind == client.EventHookDropped {
			t.Fatalf("dropped %+v", ev)
		}
	}
}
