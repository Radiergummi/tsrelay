package tunnel

import (
	"slices"
	"sync"
)

// Replacement reports hooks a tunnel lost to a newer tunnel. An orphaned
// tunnel serves no hooks any more and should be closed after it was told.
type Replacement struct {
	Tunnel   *Tunnel
	Hooks    []string
	Orphaned bool
}

// Registry tracks which tunnel serves each hook. A hook is served by at most
// one tunnel; attaching a newer tunnel replaces the older one ("takeover").
type Registry struct {
	mu     sync.Mutex
	byHook map[string]*Tunnel
	hooks  map[*Tunnel][]string
}

func NewRegistry() *Registry {
	return &Registry{byHook: map[string]*Tunnel{}, hooks: map[*Tunnel][]string{}}
}

func (r *Registry) Attach(t *Tunnel, hookIDs []string) []Replacement {
	r.mu.Lock()
	defer r.mu.Unlock()
	lost := map[*Tunnel][]string{}
	for _, h := range hookIDs {
		if old, ok := r.byHook[h]; ok && old != t {
			lost[old] = append(lost[old], h)
			r.hooks[old] = slices.DeleteFunc(r.hooks[old], func(x string) bool { return x == h })
		}
		r.byHook[h] = t
		if !slices.Contains(r.hooks[t], h) {
			r.hooks[t] = append(r.hooks[t], h)
		}
	}
	var out []Replacement
	for old, hs := range lost {
		orphaned := len(r.hooks[old]) == 0
		if orphaned {
			delete(r.hooks, old)
		}
		out = append(out, Replacement{Tunnel: old, Hooks: hs, Orphaned: orphaned})
	}
	return out
}

func (r *Registry) Lookup(hookID string) (*Tunnel, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.byHook[hookID]
	return t, ok
}

// Detach removes t and returns the hooks it was still serving.
func (r *Registry) Detach(t *Tunnel) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	hs := r.hooks[t]
	delete(r.hooks, t)
	for _, h := range hs {
		if r.byHook[h] == t {
			delete(r.byHook, h)
		}
	}
	return hs
}

// DetachHook stops serving hookID, e.g. because the hook was deleted.
func (r *Registry) DetachHook(hookID string) (t *Tunnel, orphaned, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok = r.byHook[hookID]
	if !ok {
		return nil, false, false
	}
	delete(r.byHook, hookID)
	r.hooks[t] = slices.DeleteFunc(r.hooks[t], func(x string) bool { return x == hookID })
	if len(r.hooks[t]) == 0 {
		delete(r.hooks, t)
		orphaned = true
	}
	return t, orphaned, true
}

// CloseAll closes every attached tunnel, for relay shutdown.
func (r *Registry) CloseAll() {
	r.mu.Lock()
	ts := make([]*Tunnel, 0, len(r.hooks))
	for t := range r.hooks {
		ts = append(ts, t)
	}
	r.mu.Unlock()
	for _, t := range ts {
		t.Close()
	}
}
