package tunnel

import (
	"slices"
	"testing"
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
