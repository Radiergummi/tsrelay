package tunnel

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/authz"
)

func TestRegistryTakeover(t *testing.T) {
	r := NewRegistry()
	a, b := &Tunnel{ID: "a"}, &Tunnel{ID: "b"}
	if reps := r.Attach(a, []string{"h1", "h2"}); len(reps) != 0 {
		t.Fatalf("first attach replaced %v", reps)
	}
	reps := r.Attach(b, []string{"h1"})
	if len(reps) != 1 || reps[0].Tunnel != a || !slices.Equal(reps[0].Hooks, []string{"h1"}) || reps[0].Orphaned {
		t.Fatalf("partial takeover = %+v", reps)
	}
	if got, _ := r.Lookup("h1"); got != b {
		t.Fatal("h1 should be served by b")
	}
	reps = r.Attach(b, []string{"h2"})
	if len(reps) != 1 || !reps[0].Orphaned {
		t.Fatalf("a lost its last hook, should be orphaned: %+v", reps)
	}
	if hs := r.Detach(a); len(hs) != 0 {
		t.Fatalf("orphaned tunnel still serves %v", hs)
	}
}

func TestRegistryDetach(t *testing.T) {
	r := NewRegistry()
	a, b := &Tunnel{ID: "a"}, &Tunnel{ID: "b"}
	r.Attach(a, []string{"h1", "h2"})
	r.Attach(b, []string{"h2"})
	if hs := r.Detach(a); !slices.Equal(hs, []string{"h1"}) {
		t.Fatalf("Detach(a) = %v, want [h1]", hs)
	}
	if got, ok := r.Lookup("h2"); !ok || got != b {
		t.Fatal("detaching a must not drop b's hook")
	}
	tun, orphaned, ok := r.DetachHook("h2")
	if !ok || tun != b || !orphaned {
		t.Fatalf("DetachHook = %v %v %v", tun, orphaned, ok)
	}
	if _, ok := r.Lookup("h2"); ok {
		t.Fatal("h2 still attached")
	}
}

type waitResult struct {
	t   *Tunnel
	err error
}

func startWait(ctx context.Context, r *Registry, hookID string) chan waitResult {
	c := make(chan waitResult, 1)
	go func() {
		t, err := r.Wait(ctx, hookID)
		c <- waitResult{t, err}
	}()
	return c
}

func TestRegistryWait(t *testing.T) {
	r := NewRegistry()
	a, b := &Tunnel{ID: "a"}, &Tunnel{ID: "b"}
	got := startWait(context.Background(), r, "h1")

	// An attach for a different hook must not wake the waiter.
	r.Attach(b, []string{"h2"})
	select {
	case res := <-got:
		t.Fatalf("woken by another hook's attach: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
	r.Attach(a, []string{"h1"})
	if res := <-got; res.err != nil || res.t != a {
		t.Fatalf("Wait = %+v, want tunnel a", res)
	}
	// Already attached: no waiting.
	if tun, err := r.Wait(context.Background(), "h2"); err != nil || tun != b {
		t.Fatalf("Wait(attached) = %v, %v", tun, err)
	}
}

func TestRegistryWaitEnds(t *testing.T) {
	r := NewRegistry()
	stop := errors.New("hold timeout")
	ctx, cancel := context.WithTimeoutCause(context.Background(), 20*time.Millisecond, stop)
	defer cancel()
	if _, err := r.Wait(ctx, "h1"); err != stop {
		t.Fatalf("Wait after timeout = %v, want the context's cause", err)
	}

	got := startWait(context.Background(), r, "h1")
	other := startWait(context.Background(), r, "h2")
	time.Sleep(20 * time.Millisecond) // let both block
	r.Release("h1")
	if res := <-got; !errors.Is(res.err, ErrHookGone) {
		t.Fatalf("Wait after Release = %+v, want ErrHookGone", res)
	}
	select {
	case res := <-other:
		t.Fatalf("Release of h1 woke h2's waiter: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
	r.Attach(&Tunnel{ID: "c"}, []string{"h2"})
	if res := <-other; res.err != nil {
		t.Fatal(res.err)
	}
}

func TestRegistryCountOwner(t *testing.T) {
	r := NewRegistry()
	me, other := authz.Identity{UserID: 1}, authz.Identity{UserID: 2}
	a, b, c := &Tunnel{ID: "a", Owner: me}, &Tunnel{ID: "b", Owner: me}, &Tunnel{ID: "c", Owner: other}
	r.Attach(a, []string{"h1", "h2"})
	r.Attach(b, []string{"h3"})
	r.Attach(c, []string{"h4"})
	cases := []struct {
		taking []string
		want   int
	}{
		{nil, 2},
		{[]string{"h3"}, 1},             // b would be orphaned
		{[]string{"h1"}, 2},             // a keeps h2
		{[]string{"h1", "h2", "h3"}, 0}, // both would be orphaned
		{[]string{"h4"}, 2},             // other users' tunnels never count
	}
	for _, c := range cases {
		if got := r.CountOwner(1, c.taking...); got != c.want {
			t.Errorf("CountOwner(1, %v) = %d, want %d", c.taking, got, c.want)
		}
	}
	if got := r.CountOwner(2); got != 1 {
		t.Errorf("CountOwner(2) = %d, want 1", got)
	}
	r.Detach(b)
	if got := r.CountOwner(1); got != 1 {
		t.Errorf("after Detach(b): CountOwner(1) = %d, want 1", got)
	}
}

func TestRegistrySnapshotAndDetachFrom(t *testing.T) {
	r := NewRegistry()
	a, b := &Tunnel{ID: "a"}, &Tunnel{ID: "b"}
	r.Attach(a, []string{"h1", "h2"})
	snap := r.Snapshot()
	if len(snap) != 1 || !slices.Equal(snap[a], []string{"h1", "h2"}) {
		t.Fatalf("Snapshot = %v", snap)
	}
	snap[a][0] = "changed"
	if got, ok := r.Lookup("h1"); !ok || got != a {
		t.Fatal("changing a snapshot must not change the registry")
	}

	r.Attach(b, []string{"h1"}) // b takes h1 over after the snapshot
	detached, orphaned := r.DetachFrom(a, []string{"h1", "h2"})
	if !slices.Equal(detached, []string{"h2"}) || !orphaned {
		t.Fatalf("DetachFrom = %v %v, want [h2] and orphaned", detached, orphaned)
	}
	if got, ok := r.Lookup("h1"); !ok || got != b {
		t.Fatal("a stale DetachFrom detached the hook from its new tunnel")
	}
	if detached, _ := r.DetachFrom(a, []string{"h1"}); len(detached) != 0 {
		t.Fatalf("DetachFrom of a tunnel that serves nothing = %v", detached)
	}
	if s := r.Snapshot(); len(s) != 1 || !slices.Equal(s[b], []string{"h1"}) {
		t.Fatalf("Snapshot after DetachFrom = %v", s)
	}
}
