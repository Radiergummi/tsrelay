package lifecycle

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"github.com/Radiergummi/tsrelay/internal/tunnel/tunneltest"
	"tailscale.com/tailcfg"
)

var receiver = identify.User("laptop", "moritz@example.com", 1, `{"roles":["receiver"]}`)

// newLifecycle returns a Lifecycle over a real hook store.
func newLifecycle(t *testing.T) (*Lifecycle, *hooks.Manager) {
	t.Helper()
	store, err := hooks.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	mgr, err := hooks.NewManager(context.Background(), store, hooks.SlugConfig{})
	if err != nil {
		t.Fatal(err)
	}
	al := audit.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	return New(Config{Hooks: mgr, Audit: al, BaseURL: "https://relay", PublicBaseURL: "https://public"}), mgr
}

// createHooks creates n hooks from req and returns their IDs.
func createHooks(t *testing.T, mgr *hooks.Manager, n int, req hooks.CreateRequest) []hooks.ID {
	t.Helper()
	var out []hooks.ID
	for range n {
		h, err := mgr.Create(context.Background(), receiver, req)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, h.ID)
	}
	return out
}

// waitFor waits for cond to hold; tunnel goroutines settle asynchronously.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(time.Millisecond)
	}
}

func (l *Lifecycle) attach(t *tunnel.Tunnel, ids ...hooks.ID) []replacement {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.attachLocked(t, ids)
}

func (l *Lifecycle) detach(t *tunnel.Tunnel) []hooks.ID {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.detachLocked(t)
}

func (l *Lifecycle) countOwner(user tailcfg.UserID, taking ...hooks.ID) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.countOwnerLocked(user, taking)
}

func TestTakeover(t *testing.T) {
	l, _ := newLifecycle(t)
	a, b := &tunnel.Tunnel{ID: "a"}, &tunnel.Tunnel{ID: "b"}
	if reps := l.attach(a, "h1", "h2"); len(reps) != 0 {
		t.Fatalf("first attach replaced %v", reps)
	}
	reps := l.attach(b, "h1")
	if len(reps) != 1 || reps[0].tunnel != a || !slices.Equal(reps[0].hooks, []hooks.ID{"h1"}) || reps[0].orphaned {
		t.Fatalf("partial takeover = %+v", reps)
	}
	if got, _ := l.Lookup("h1"); got != b {
		t.Fatal("h1 should be served by b")
	}
	reps = l.attach(b, "h2")
	if len(reps) != 1 || !reps[0].orphaned {
		t.Fatalf("a lost its last hook, should be orphaned: %+v", reps)
	}
	if hs := l.detach(a); len(hs) != 0 {
		t.Fatalf("orphaned tunnel still serves %v", hs)
	}
}

func TestDetach(t *testing.T) {
	l, _ := newLifecycle(t)
	a, b := &tunnel.Tunnel{ID: "a"}, &tunnel.Tunnel{ID: "b"}
	l.attach(a, "h1", "h2")
	l.attach(b, "h2")
	if hs := l.detach(a); !slices.Equal(hs, []hooks.ID{"h1"}) {
		t.Fatalf("detach(a) = %v, want [h1]", hs)
	}
	if got, ok := l.Lookup("h2"); !ok || got != b {
		t.Fatal("detaching a must not drop b's hook")
	}
	l.mu.Lock()
	tun, orphaned, ok := l.detachHookLocked("h2")
	l.mu.Unlock()
	if !ok || tun != b || !orphaned {
		t.Fatalf("detachHookLocked = %v %v %v", tun, orphaned, ok)
	}
	if _, ok := l.Lookup("h2"); ok {
		t.Fatal("h2 still attached")
	}
}

type waitResult struct {
	t   *tunnel.Tunnel
	err error
}

func startWait(ctx context.Context, l *Lifecycle, id hooks.ID) chan waitResult {
	c := make(chan waitResult, 1)
	go func() {
		t, err := l.Wait(ctx, id)
		c <- waitResult{t, err}
	}()
	return c
}

// release fails the waiters of id, as deleting the hook will.
func (l *Lifecycle) release(id hooks.ID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.wakeLocked(id, true)
}

func TestWait(t *testing.T) {
	l, mgr := newLifecycle(t)
	ids := createHooks(t, mgr, 2, hooks.CreateRequest{})
	h1, h2 := ids[0], ids[1]
	a, b := &tunnel.Tunnel{ID: "a"}, &tunnel.Tunnel{ID: "b"}
	got := startWait(context.Background(), l, h1)

	// An attach for a different hook must not wake the waiter.
	l.attach(b, h2)
	select {
	case res := <-got:
		t.Fatalf("woken by another hook's attach: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
	l.attach(a, h1)
	if res := <-got; res.err != nil || res.t != a {
		t.Fatalf("Wait = %+v, want tunnel a", res)
	}
	// Already attached: no waiting.
	if tun, err := l.Wait(context.Background(), h2); err != nil || tun != b {
		t.Fatalf("Wait(attached) = %v, %v", tun, err)
	}
}

// Wait for a hook the store does not have fails at once: a hook deleted
// before the waiter registered cannot strand it.
func TestWaitFailsForMissingHook(t *testing.T) {
	l, _ := newLifecycle(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := l.Wait(ctx, "h_missing"); !errors.Is(err, ErrHookGone) {
		t.Fatalf("Wait for a missing hook = %v, want ErrHookGone", err)
	}
}

func TestWaitEnds(t *testing.T) {
	l, mgr := newLifecycle(t)
	ids := createHooks(t, mgr, 2, hooks.CreateRequest{})
	h1, h2 := ids[0], ids[1]
	stop := errors.New("hold timeout")
	ctx, cancel := context.WithTimeoutCause(context.Background(), 20*time.Millisecond, stop)
	defer cancel()
	if _, err := l.Wait(ctx, h1); err != stop {
		t.Fatalf("Wait after timeout = %v, want the context's cause", err)
	}

	got := startWait(context.Background(), l, h1)
	other := startWait(context.Background(), l, h2)
	time.Sleep(20 * time.Millisecond) // let both block
	l.release(h1)
	if res := <-got; !errors.Is(res.err, ErrHookGone) {
		t.Fatalf("Wait after release = %+v, want ErrHookGone", res)
	}
	select {
	case res := <-other:
		t.Fatalf("release of h1 woke h2's waiter: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
	l.attach(&tunnel.Tunnel{ID: "c"}, h2)
	if res := <-other; res.err != nil {
		t.Fatal(res.err)
	}
}

// Waits that end without an attach must not leave entries behind, and one
// waiter giving up must not strand another waiting for the same hook.
func TestWaitLeavesNoEntries(t *testing.T) {
	l, mgr := newLifecycle(t)
	ids := createHooks(t, mgr, 2, hooks.CreateRequest{})
	timeout, shared := ids[0], ids[1]
	short, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := l.Wait(short, timeout); err == nil {
		t.Fatal("Wait returned without an attach")
	}
	if _, err := l.Wait(context.Background(), "h_deleted"); !errors.Is(err, ErrHookGone) {
		t.Fatalf("Wait for a deleted hook = %v, want ErrHookGone", err)
	}

	long := startWait(context.Background(), l, shared)
	time.Sleep(20 * time.Millisecond) // let it block
	brief, cancelBrief := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelBrief()
	if _, err := l.Wait(brief, shared); err == nil {
		t.Fatal("the brief Wait returned without an attach")
	}
	a := &tunnel.Tunnel{ID: "a"}
	l.attach(a, shared)
	if res := <-long; res.err != nil || res.t != a {
		t.Fatalf("remaining waiter = %+v, want tunnel a", res)
	}

	l.mu.Lock()
	left := len(l.waits)
	l.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d waiter entries left behind", left)
	}
}

func TestCountOwner(t *testing.T) {
	l, _ := newLifecycle(t)
	me, other := authz.Identity{UserID: 1}, authz.Identity{UserID: 2}
	a, b, c := &tunnel.Tunnel{ID: "a", Owner: me}, &tunnel.Tunnel{ID: "b", Owner: me}, &tunnel.Tunnel{ID: "c", Owner: other}
	l.attach(a, "h1", "h2")
	l.attach(b, "h3")
	l.attach(c, "h4")
	cases := []struct {
		taking []hooks.ID
		want   int
	}{
		{nil, 2},
		{[]hooks.ID{"h3"}, 1},             // b would be orphaned
		{[]hooks.ID{"h1"}, 2},             // a keeps h2
		{[]hooks.ID{"h1", "h2", "h3"}, 0}, // both would be orphaned
		{[]hooks.ID{"h4"}, 2},             // other users' tunnels never count
	}
	for _, c := range cases {
		if got := l.countOwner(1, c.taking...); got != c.want {
			t.Errorf("countOwner(1, %v) = %d, want %d", c.taking, got, c.want)
		}
	}
	if got := l.countOwner(2); got != 1 {
		t.Errorf("countOwner(2) = %d, want 1", got)
	}
	l.detach(b)
	if got := l.countOwner(1); got != 1 {
		t.Errorf("after detach(b): countOwner(1) = %d, want 1", got)
	}
}

func TestSnapshotAndDetachFrom(t *testing.T) {
	l, _ := newLifecycle(t)
	a, b := &tunnel.Tunnel{ID: "a"}, &tunnel.Tunnel{ID: "b"}
	l.attach(a, "h1", "h2")
	snap := l.Snapshot()
	if len(snap) != 1 || !slices.Equal(snap[a], []hooks.ID{"h1", "h2"}) {
		t.Fatalf("Snapshot = %v", snap)
	}
	snap[a][0] = "changed"
	if got, ok := l.Lookup("h1"); !ok || got != a {
		t.Fatal("changing a snapshot must not change the registry")
	}

	l.attach(b, "h1") // b takes h1 over after the snapshot
	l.mu.Lock()
	detached, orphaned := l.detachFromLocked(a, []hooks.ID{"h1", "h2"})
	l.mu.Unlock()
	if !slices.Equal(detached, []hooks.ID{"h2"}) || !orphaned {
		t.Fatalf("detachFromLocked = %v %v, want [h2] and orphaned", detached, orphaned)
	}
	if got, ok := l.Lookup("h1"); !ok || got != b {
		t.Fatal("a stale detachFrom detached the hook from its new tunnel")
	}
	l.mu.Lock()
	detached, _ = l.detachFromLocked(a, []hooks.ID{"h1"})
	l.mu.Unlock()
	if len(detached) != 0 {
		t.Fatalf("detachFrom of a tunnel that serves nothing = %v", detached)
	}
	if s := l.Snapshot(); len(s) != 1 || !slices.Equal(s[b], []hooks.ID{"h1"}) {
		t.Fatalf("Snapshot after detachFrom = %v", s)
	}
}

func TestHookInfo(t *testing.T) {
	l, mgr := newLifecycle(t)
	h, err := mgr.Create(context.Background(), receiver, hooks.CreateRequest{Path: "cb"})
	if err != nil {
		t.Fatal(err)
	}
	info := l.hookInfo(h)
	if info.ID != string(h.ID) || info.URL != "https://relay"+h.Route() || info.Path != "cb" || info.Public || info.PublicURL != "" {
		t.Fatalf("hookInfo = %+v", info)
	}
	h.Public, h.PublicToken = true, "tok"
	info = l.hookInfo(h)
	if !info.Public || info.PublicURL != "https://public/+tok" {
		t.Fatalf("public hookInfo = %+v", info)
	}
	l.cfg.PublicBaseURL = ""
	if info = l.hookInfo(h); info.PublicURL != "" {
		t.Fatalf("PublicURL without Funnel = %q", info.PublicURL)
	}
}

func TestClose(t *testing.T) {
	l, _ := newLifecycle(t)
	a, _ := tunneltest.Pair(t, http.NotFoundHandler(), tunnel.Options{}, nil)
	l.attach(a, "h1")
	l.Close()
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Close left a tunnel open")
	}
	if got := a.CloseReason(); got != "shutdown" {
		t.Fatalf("CloseReason = %q, want shutdown", got)
	}
}
