package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
		panic("unreachable")
	}
}

// startClient attaches one tunnel for hookIDs as the receiver, the way
// `tsrelay attach` does. It returns once the relay reported the attach.
func (e *env) startClient(hookIDs ...string) (controls chan tunnel.Control, done chan error) {
	e.t.Helper()
	controls, done = make(chan tunnel.Control, 8), make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	e.t.Cleanup(cancel)
	tc := &tunnel.Client{
		Hooks: hookIDs,
		Dial: func(ctx context.Context, hs []string, a tunnel.Attach) (net.Conn, error) {
			return tunnel.Dial(ctx, e.api.HTTP, e.api.TunnelURL(a, hs...))
		},
		Handler:    http.NotFoundHandler(),
		OnControl:  func(c tunnel.Control) { controls <- c },
		MinBackoff: time.Millisecond,
	}
	go func() { done <- tc.Run(ctx) }()
	if c := receive(e.t, controls); c.Type != tunnel.ControlAttached {
		e.t.Fatalf("first control = %+v, want attached", c)
	}
	return controls, done
}

func TestRevalidateRevokesLostChannels(t *testing.T) {
	e := newEnv(t, Config{})
	ctx := context.Background()
	keep, _ := e.api.CreateHook(ctx, hooks.CreateRequest{Path: "keep"})
	lose, _ := e.api.CreateHook(ctx, hooks.CreateRequest{Path: "lose", Channel: "prod"})
	controls, _ := e.startClient(keep.ID, lose.ID)

	e.srv.revalidate(ctx)
	if _, ok := e.srv.tunnels.Lookup(lose.ID); !ok {
		t.Fatal("revalidation revoked a hook whose grants did not change")
	}

	e.fake.Update(identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"],"channels":["default"]}`))
	e.srv.revalidate(ctx)
	if c := receive(t, controls); c.Type != tunnel.ControlRevoked || len(c.Hooks) != 1 || c.Hooks[0].ID != lose.ID {
		t.Fatalf("control = %+v, want revoked for %s only", c, lose.ID)
	}
	if _, ok := e.srv.tunnels.Lookup(lose.ID); ok {
		t.Fatal("the hook whose channel was revoked is still attached")
	}
	if _, ok := e.srv.tunnels.Lookup(keep.ID); !ok {
		t.Fatal("the hook whose channel is still granted was detached")
	}
	// The hook is not deleted: it delivers again once the grant is back.
	if _, ok := e.srv.hooks.Get(hooks.ID(lose.ID)); !ok {
		t.Fatal("revocation deleted the hook")
	}
	if !strings.Contains(e.log.String(), `"msg":"tunnel.revoked"`) {
		t.Fatal("revocation was not audited")
	}
}

func TestRevalidateRevokesTunnel(t *testing.T) {
	e := newEnv(t, Config{})
	hook, _ := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	controls, done := e.startClient(hook.ID)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.srv.RunRevalidator(ctx, 10*time.Millisecond)

	e.fake.Update(identify.User("laptop", "moritz@example.com", 1, `{"roles":["sender"]}`))
	if c := receive(t, controls); c.Type != tunnel.ControlRevoked || len(c.Hooks) != 1 || c.Hooks[0].ID != hook.ID {
		t.Fatalf("control = %+v, want revoked", c)
	}
	if err := receive(t, done); !errors.Is(err, tunnel.ErrNoHooks) {
		t.Fatalf("client Run = %v, want ErrNoHooks: the relay closes a fully revoked tunnel", err)
	}
	if _, ok := e.srv.tunnels.Lookup(hook.ID); ok {
		t.Fatal("the revoked hook is still attached")
	}
}

// Review Focus: a revalidation result computed for tunnel T1 and applied
// after T2 took the hook over must not revoke the hook from T2.
func TestRevalidationRacingTakeover(t *testing.T) {
	e := newEnv(t, Config{})
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer local.Close()
	hook, _ := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	defer e.cli(hook.ID, local.URL)()

	// The revalidator takes its snapshot while T1 serves the hook...
	var t1 *tunnel.Tunnel
	var t1Hooks []string
	for tun, hs := range e.srv.tunnels.Snapshot() {
		t1, t1Hooks = tun, hs
	}
	// ...then T2 takes the hook over...
	defer e.cli(hook.ID, local.URL)()
	e.waitFor(func() bool {
		cur, ok := e.srv.tunnels.Lookup(hook.ID)
		return ok && cur != t1
	})
	t2, _ := e.srv.tunnels.Lookup(hook.ID)
	// ...and only then is the (revoking) result for T1 applied.
	e.srv.revoke(context.Background(), t1, t1Hooks, "test")

	if cur, ok := e.srv.tunnels.Lookup(hook.ID); !ok || cur != t2 {
		t.Fatal("a stale revocation of T1 detached the hook from T2")
	}
	if resp := e.send(hook, "", nil, nil); resp.StatusCode != 204 {
		t.Fatalf("call through T2 = %d, want 204", resp.StatusCode)
	}
	if strings.Contains(e.log.String(), `"msg":"tunnel.revoked"`) {
		t.Fatal("a revocation that detached nothing was audited")
	}
}
