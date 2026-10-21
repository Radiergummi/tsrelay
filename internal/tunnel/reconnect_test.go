package tunnel_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// fakeRelay accepts tunnels; reject, if set, answers the first N attempts.
type fakeRelay struct {
	srv     *httptest.Server
	tunnels chan *tunnel.Tunnel
	reject  atomic.Int32
	status  int
}

func newFakeRelay(t *testing.T, rejectStatus int, rejections int32) *fakeRelay {
	f := &fakeRelay{tunnels: make(chan *tunnel.Tunnel, 4), status: rejectStatus}
	f.reject.Store(rejections)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.reject.Add(-1) >= 0 {
			problem.New(f.status, "test", "rejected").Write(w)
			return
		}
		conn, err := tunnel.Accept(w, r)
		if err != nil {
			t.Error(err)
			return
		}
		tun, err := tunnel.NewTunnel(conn, "t", authz.Identity{}, tunnel.Options{})
		if err != nil {
			t.Error(err)
			return
		}
		f.tunnels <- tun
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRelay) client(states chan<- bool) *tunnel.Client {
	return &tunnel.Client{
		Hooks: []string{"h1"},
		Dial: func(ctx context.Context, hooks []string, _ tunnel.Attach) (net.Conn, error) {
			return tunnel.Dial(ctx, tunnel.NewHTTPClient(nil), f.srv.URL+"/?hook="+hooks[0])
		},
		Handler:    http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(299) }),
		OnState:    func(connected bool, _ error) { states <- connected },
		MinBackoff: time.Millisecond,
		MaxBackoff: 10 * time.Millisecond,
	}
}

func recv[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
		panic("unreachable")
	}
}

func run(t *testing.T, c *tunnel.Client) (context.CancelFunc, <-chan error) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	t.Cleanup(cancel)
	return cancel, done
}

func TestClientReconnects(t *testing.T) {
	f := newFakeRelay(t, 0, 0)
	states := make(chan bool, 16)
	run(t, f.client(states))

	first := recv(t, f.tunnels)
	if !recv(t, states) {
		t.Fatal("first state should be connected")
	}
	first.Close()
	if recv(t, states) {
		t.Fatal("expected a disconnected state after the tunnel dropped")
	}
	second := recv(t, f.tunnels)
	req, _ := http.NewRequest("GET", "http://tunnel/", nil)
	resp, err := second.RoundTrip(req)
	if err != nil || resp.StatusCode != 299 {
		t.Fatalf("after reconnect: %v %v", resp, err)
	}
	resp.Body.Close()
}

// A relay that accepts each attach and then drops it at once is redialled
// with backoff, not in a tight loop.
func TestClientBacksOffAfterInstantDrops(t *testing.T) {
	var dials atomic.Int32
	c := &tunnel.Client{
		Hooks: []string{"h1"},
		Dial: func(context.Context, []string, tunnel.Attach) (net.Conn, error) {
			dials.Add(1)
			conn, relay := net.Pipe()
			relay.Close()
			return conn, nil
		},
		Handler:    http.NotFoundHandler(),
		MinBackoff: 20 * time.Millisecond,
		MaxBackoff: time.Second,
	}
	cancel, done := run(t, c)
	time.Sleep(300 * time.Millisecond)
	cancel()
	recv(t, done)
	if n := dials.Load(); n > 8 {
		t.Fatalf("%d dials in 300ms, want backoff", n)
	}
}

func TestClientRetriesServerErrors(t *testing.T) {
	f := newFakeRelay(t, http.StatusServiceUnavailable, 2)
	run(t, f.client(make(chan bool, 16)))
	recv(t, f.tunnels)
}

func TestClientStopsOnClientErrors(t *testing.T) {
	f := newFakeRelay(t, http.StatusForbidden, 1000)
	_, done := run(t, f.client(make(chan bool, 16)))
	var p *problem.Problem
	if err := recv(t, done); !errors.As(err, &p) || p.Status != 403 {
		t.Fatalf("Run = %v, want the 403 problem", err)
	}
}

// A CLI whose only hook was taken over must stop rather than reconnect and
// steal the hook back.
func TestClientStopsWhenReplaced(t *testing.T) {
	f := newFakeRelay(t, 0, 0)
	_, done := run(t, f.client(make(chan bool, 16)))
	tun := recv(t, f.tunnels)
	if err := tun.Send(context.Background(), tunnel.Control{Type: tunnel.ControlReplaced, Hooks: []tunnel.HookInfo{{ID: "h1"}}}); err != nil {
		t.Fatal(err)
	}
	tun.Close()
	if err := recv(t, done); !errors.Is(err, tunnel.ErrNoHooks) {
		t.Fatalf("Run = %v, want ErrNoHooks", err)
	}
	select {
	case <-f.tunnels:
		t.Fatal("client reconnected after being replaced")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestClientStopsOnCancel(t *testing.T) {
	f := newFakeRelay(t, 0, 0)
	cancel, done := run(t, f.client(make(chan bool, 16)))
	recv(t, f.tunnels)
	cancel()
	if err := recv(t, done); err != nil {
		t.Fatalf("Run = %v, want nil after cancel", err)
	}
}

// Every dial of one Client uses the same session; only dials after a
// successful connection are resumes.
func TestClientSessionAndResume(t *testing.T) {
	f := newFakeRelay(t, http.StatusServiceUnavailable, 1)
	attaches := make(chan tunnel.Attach, 8)
	c := f.client(make(chan bool, 16))
	dial := c.Dial
	c.Dial = func(ctx context.Context, hooks []string, a tunnel.Attach) (net.Conn, error) {
		attaches <- a
		return dial(ctx, hooks, a)
	}
	run(t, c)
	first, second := recv(t, attaches), recv(t, attaches)
	recv(t, f.tunnels).Close()
	third := recv(t, attaches)
	if first.Session == "" || first.Session != second.Session || second.Session != third.Session {
		t.Fatalf("sessions differ: %q %q %q", first.Session, second.Session, third.Session)
	}
	if first.Resume || second.Resume || !third.Resume {
		t.Fatalf("resume flags = %v %v %v, want false false true", first.Resume, second.Resume, third.Resume)
	}
}

// Revoked hooks are dropped like replaced ones and reported through
// OnControl, so the CLI stops instead of reattaching them.
func TestClientDropsRevokedHooks(t *testing.T) {
	f := newFakeRelay(t, 0, 0)
	c := f.client(make(chan bool, 16))
	controls := make(chan tunnel.Control, 1)
	c.OnControl = func(ctl tunnel.Control) { controls <- ctl }
	_, done := run(t, c)
	tun := recv(t, f.tunnels)
	if err := tun.Send(context.Background(), tunnel.Control{Type: tunnel.ControlRevoked, Hooks: []tunnel.HookInfo{{ID: "h1"}}}); err != nil {
		t.Fatal(err)
	}
	if got := recv(t, controls); got.Type != tunnel.ControlRevoked || len(got.Hooks) != 1 || got.Hooks[0].ID != "h1" {
		t.Fatalf("OnControl got %+v, want the revoked control", got)
	}
	tun.Close()
	if err := recv(t, done); !errors.Is(err, tunnel.ErrNoHooks) {
		t.Fatalf("Run = %v, want ErrNoHooks", err)
	}
}

// A client with List dials what its owner says, every time, and leaves the
// hooks control messages take away to the owner, which hears of them in
// OnControl: one list, so a redial cannot attach a hook the owner dropped.
func TestClientList(t *testing.T) {
	f := newFakeRelay(t, 0, 0)
	c := f.client(make(chan bool, 16))
	var mu sync.Mutex
	list := []string{"h1"}
	c.Hooks = nil
	c.List = func() []string { mu.Lock(); defer mu.Unlock(); return slices.Clone(list) }
	dials := make(chan []string, 4)
	dial := c.Dial
	c.Dial = func(ctx context.Context, hooks []string, a tunnel.Attach) (net.Conn, error) {
		dials <- slices.Clone(hooks)
		return dial(ctx, hooks, a)
	}
	controls := make(chan tunnel.Control, 4)
	c.OnControl = func(ctl tunnel.Control) { controls <- ctl }
	_, done := run(t, c)
	if got := recv(t, dials); !slices.Equal(got, []string{"h1"}) {
		t.Fatalf("first dial = %v", got)
	}
	tun := recv(t, f.tunnels)
	mu.Lock()
	list = []string{"h2"}
	mu.Unlock()
	if err := tun.Send(context.Background(), tunnel.Control{Type: tunnel.ControlReplaced, Hooks: []tunnel.HookInfo{{ID: "h2"}}}); err != nil {
		t.Fatal(err)
	}
	recv(t, controls)
	tun.Close()
	if got := recv(t, dials); !slices.Equal(got, []string{"h2"}) {
		t.Fatalf("redial = %v, want the owner's [h2] despite the control message", got)
	}
	tun = recv(t, f.tunnels)
	mu.Lock()
	list = nil
	mu.Unlock()
	tun.Close()
	if err := recv(t, done); !errors.Is(err, tunnel.ErrNoHooks) {
		t.Fatalf("Run = %v, want ErrNoHooks once the owner's list is empty", err)
	}
}

// A dial that fails after its owner dropped the last hook ends Run with
// ErrNoHooks at once, without reporting a lost connection first.
func TestClientNoHooksAfterFailedDial(t *testing.T) {
	f := newFakeRelay(t, http.StatusServiceUnavailable, 0)
	states := make(chan bool, 16)
	c := f.client(states)
	var list atomic.Pointer[[]string]
	list.Store(&[]string{"h1"})
	c.List = func() []string { return *list.Load() }
	c.Dial = func(context.Context, []string, tunnel.Attach) (net.Conn, error) {
		list.Store(&[]string{})
		return nil, errors.New("down")
	}
	_, done := run(t, c)
	if err := recv(t, done); !errors.Is(err, tunnel.ErrNoHooks) {
		t.Fatalf("Run = %v, want ErrNoHooks", err)
	}
	if len(states) != 0 {
		t.Fatalf("OnState was called %d times, want none", len(states))
	}
}
