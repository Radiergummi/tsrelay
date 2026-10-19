package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

const grace = 10 * time.Minute

// at makes l's clock read t0+d, where t0 is the hooks' creation time. Tunnel
// goroutines read the clock, so it changes under the lock.
func at(l *Lifecycle, t0 time.Time, d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.now = func() time.Time { return t0.Add(d) }
}

func createEphemeral(t *testing.T, mgr *hooks.Manager, n int) []hooks.Hook {
	t.Helper()
	var out []hooks.Hook
	for range n {
		h, err := mgr.Create(context.Background(), receiver, hooks.CreateRequest{Ephemeral: true})
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, h)
	}
	return out
}

func stored(mgr *hooks.Manager, id hooks.ID) bool {
	_, ok := mgr.Get(id)
	return ok
}

// waitDetached waits for the end of a tunnel to detach its hook.
func waitDetached(t *testing.T, l *Lifecycle, id hooks.ID) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := l.Lookup(id); !ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the hook stayed attached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestReap(t *testing.T) {
	l, mgr := newLifecycle(t)
	eph := createEphemeral(t, mgr, 2)
	per := createHooks(t, mgr, 1)[0]
	t0 := eph[0].CreatedAt
	l.started = t0
	att, loose := eph[0], eph[1]
	tun, drop, _ := controlPair(t)
	admitAttach(t, l, receiver, tun, att.ID)

	at(l, t0, 9*time.Minute)
	if err := l.Reap(context.Background(), grace); err != nil || !stored(mgr, loose.ID) {
		t.Fatalf("Reap before grace elapsed = %v, loose hook stored = %v", err, stored(mgr, loose.ID))
	}
	at(l, t0, 11*time.Minute)
	if err := l.Reap(context.Background(), grace); err != nil {
		t.Fatal(err)
	}
	if stored(mgr, loose.ID) || !stored(mgr, att.ID) || !stored(mgr, per) {
		t.Fatal("only the unattached ephemeral hook should be reaped")
	}

	drop()
	waitDetached(t, l, att.ID)
	at(l, t0, 22*time.Minute)
	if err := l.Reap(context.Background(), grace); err != nil {
		t.Fatal(err)
	}
	if stored(mgr, att.ID) || !stored(mgr, per) {
		t.Fatal("the detached hook should be reaped, the persistent one kept")
	}
}

// After a relay restart, ephemeral hooks get a full grace period again.
func TestReapGraceStartsAtStartup(t *testing.T) {
	l, mgr := newLifecycle(t)
	h := createEphemeral(t, mgr, 1)[0]
	l.started = h.CreatedAt.Add(time.Hour)
	at(l, l.started, grace-time.Second)
	if err := l.Reap(context.Background(), grace); err != nil || !stored(mgr, h.ID) {
		t.Fatalf("reaped right after startup: %v", err)
	}
	at(l, l.started, grace)
	if err := l.Reap(context.Background(), grace); err != nil || stored(mgr, h.ID) {
		t.Fatalf("Reap at grace = %v, stored = %v", err, stored(mgr, h.ID))
	}
}

// A hook a newer tunnel took over is served, however the old tunnel ended.
func TestReapSkipsTakenOverHook(t *testing.T) {
	l, mgr := newLifecycle(t)
	logs, _ := observe(l)
	h := createEphemeral(t, mgr, 1)[0]
	t0 := h.CreatedAt
	l.started = t0
	a, dropA, _ := controlPair(t)
	b, dropB, _ := controlPair(t)
	admitAttach(t, l, receiver, a, h.ID)
	admitAttach(t, l, receiver, b, h.ID)
	at(l, t0, 0)
	dropA()
	<-a.Done()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		l.mu.Lock()
		_, serving := l.served[a] // a's detach has run once it serves nothing
		l.mu.Unlock()
		if !serving {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a's detach never ran")
		}
	}

	at(l, t0, time.Hour)
	if err := l.Reap(context.Background(), grace); err != nil || !stored(mgr, h.ID) {
		t.Fatalf("reaped a hook b serves: %v", err)
	}
	dropB()
	waitDetached(t, l, h.ID)
	at(l, t0, time.Hour+grace-time.Second)
	if err := l.Reap(context.Background(), grace); err != nil || !stored(mgr, h.ID) {
		t.Fatalf("grace counted from before b dropped: %v", err)
	}
	at(l, t0, time.Hour+grace)
	if err := l.Reap(context.Background(), grace); err != nil || stored(mgr, h.ID) {
		t.Fatalf("Reap at drop+grace = %v, stored = %v", err, stored(mgr, h.ID))
	}
	if n := strings.Count(logs.String(), `"msg":"hook.expire"`); n != 1 {
		t.Fatalf("hook.expire audit lines = %d in %s", n, logs.String())
	}
}

func TestReapExpiredEvenWhileServed(t *testing.T) {
	l, mgr := newLifecycle(t)
	logs, _ := observe(l)
	exp := time.Now().Add(time.Minute)
	h, err := mgr.Create(context.Background(), receiver, hooks.CreateRequest{ExpiresAt: &exp})
	if err != nil {
		t.Fatal(err)
	}
	tun, _, controls := controlPair(t)
	admitAttach(t, l, receiver, tun, h.ID)
	next(t, controls) // attached
	unserved, err := mgr.Create(context.Background(), receiver, hooks.CreateRequest{ExpiresAt: &exp})
	if err != nil {
		t.Fatal(err)
	}
	held := startWait(context.Background(), l, unserved.ID)
	time.Sleep(20 * time.Millisecond) // let it block

	at(l, exp, time.Second)
	if err := l.Reap(context.Background(), grace); err != nil {
		t.Fatal(err)
	}
	if stored(mgr, h.ID) {
		t.Fatal("the expired hook is still stored")
	}
	if c := next(t, controls); c.Type != tunnel.ControlHookDeleted || c.Hooks[0].ID != string(h.ID) {
		t.Fatalf("control = %+v, want hook-deleted", c)
	}
	if n := strings.Count(logs.String(), `"msg":"hook.expire"`); n != 2 { // one per hook
		t.Fatalf("hook.expire audit lines = %d in %s", n, logs.String())
	}
	if strings.Contains(logs.String(), `"msg":"hook.delete"`) {
		t.Fatal("reaping logged hook.delete")
	}
	if res := <-held; !errors.Is(res.err, ErrHookGone) {
		t.Fatalf("held Wait = %+v", res)
	}
}

// A hook that becomes ephemeral long after it lost its tunnel has been
// without one for more than grace, and goes on the next pass.
func TestReapAfterBecomingEphemeral(t *testing.T) {
	l, mgr := newLifecycle(t)
	id := createHooks(t, mgr, 1)[0]
	t0 := hookOf(t, mgr, id).CreatedAt
	l.started = t0
	tun, drop, _ := controlPair(t)
	admitAttach(t, l, receiver, tun, id)
	at(l, t0, 0)
	drop()
	waitDetached(t, l, id)

	at(l, t0, 2*grace)
	yes := true
	if _, _, err := mgr.Update(context.Background(), receiver, id, hooks.UpdateRequest{Ephemeral: hooks.Opt[bool]{Set: true, Value: &yes}}); err != nil {
		t.Fatal(err)
	}
	if err := l.Reap(context.Background(), grace); err != nil {
		t.Fatal(err)
	}
	if stored(mgr, id) {
		t.Fatal("the hook should be reaped on the next pass")
	}
}
