package tunnel

import (
	"context"
	"errors"
	"slices"
	"sync"

	"tailscale.com/tailcfg"
)

// ErrHookGone is returned by Wait when the hook was deleted while waiting.
var ErrHookGone = errors.New("tunnel: the hook was deleted")

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
	waits  map[string]*waiters
}

// waiters are the calls waiting for a tunnel to attach to one hook. done
// is closed, and the entry removed, when one attaches or the hook is gone.
// n counts the waiters; the last one to leave without a wake-up removes
// the entry.
type waiters struct {
	done chan struct{}
	gone bool
	n    int
}

func NewRegistry() *Registry {
	return &Registry{byHook: map[string]*Tunnel{}, hooks: map[*Tunnel][]string{}, waits: map[string]*waiters{}}
}

func (r *Registry) Attach(t *Tunnel, hookIDs []string) []Replacement {
	r.mu.Lock()
	defer r.mu.Unlock()
	lost := map[*Tunnel][]string{}
	for _, h := range hookIDs {
		r.wakeLocked(h, false)
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

// RemoveHook fails the calls held for hookID and tells the tunnel serving
// it, if any, that the hook was deleted, closing that tunnel if it serves
// no other hooks. Call it once the hook is deleted.
func (r *Registry) RemoveHook(hookID string) {
	r.Release(hookID)
	if t, orphaned, ok := r.DetachHook(hookID); ok {
		t.Notify(Control{Type: ControlHookDeleted, Hooks: []HookInfo{{ID: hookID}}}, orphaned)
	}
}

// CountOwner returns how many tunnels of user serve hooks. A tunnel whose
// hooks are all among taking is left out: attaching those hooks to a new
// tunnel takes them over and orphans it.
func (r *Registry) CountOwner(user tailcfg.UserID, taking ...string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for t, hs := range r.hooks {
		keeps := slices.ContainsFunc(hs, func(h string) bool { return !slices.Contains(taking, h) })
		if t.Owner.UserID == user && keeps {
			n++
		}
	}
	return n
}

// Snapshot returns every attached tunnel with the hooks it serves.
func (r *Registry) Snapshot() map[*Tunnel][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[*Tunnel][]string, len(r.hooks))
	for t, hs := range r.hooks {
		out[t] = slices.Clone(hs)
	}
	return out
}

// DetachFrom stops t serving those of hookIDs it still serves, and returns
// them. Hooks another tunnel has taken over meanwhile stay with that
// tunnel. orphaned reports whether t now serves no hooks.
func (r *Registry) DetachFrom(t *Tunnel, hookIDs []string) (detached []string, orphaned bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, h := range hookIDs {
		if r.byHook[h] != t {
			continue
		}
		delete(r.byHook, h)
		r.hooks[t] = slices.DeleteFunc(r.hooks[t], func(x string) bool { return x == h })
		detached = append(detached, h)
	}
	if len(detached) > 0 && len(r.hooks[t]) == 0 {
		delete(r.hooks, t)
		orphaned = true
	}
	return detached, orphaned
}

// Wait returns the tunnel serving hookID, waiting for one to attach if
// necessary. Only an attach for hookID itself wakes it. It fails with
// ErrHookGone if Release is called for the hook, or if exists (which may be
// nil) reports it gone once the wait is registered, and with the context's
// cause when ctx is done. Deleting a hook before releasing it therefore
// cannot strand a waiter that registers between the two.
func (r *Registry) Wait(ctx context.Context, hookID string, exists func() bool) (*Tunnel, error) {
	for {
		r.mu.Lock()
		if t, ok := r.byHook[hookID]; ok {
			r.mu.Unlock()
			return t, nil
		}
		w := r.waits[hookID]
		if w == nil {
			w = &waiters{done: make(chan struct{})}
			r.waits[hookID] = w
		}
		w.n++
		r.mu.Unlock()
		if exists != nil && !exists() {
			r.leave(hookID, w)
			return nil, ErrHookGone
		}
		select {
		case <-w.done:
			r.leave(hookID, w)
			if w.gone {
				return nil, ErrHookGone
			}
			// A tunnel attached; it may have gone again before we looked.
		case <-ctx.Done():
			r.leave(hookID, w)
			return nil, context.Cause(ctx)
		}
	}
}

// leave unregisters one waiter of w, and removes w once nobody waits on it.
// An entry that was woken has already been removed or replaced.
func (r *Registry) leave(hookID string, w *waiters) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if w.n--; w.n == 0 && r.waits[hookID] == w {
		delete(r.waits, hookID)
	}
}

// Release fails every Wait for hookID with ErrHookGone. Call it when the
// hook is deleted.
func (r *Registry) Release(hookID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.wakeLocked(hookID, true)
}

func (r *Registry) wakeLocked(hookID string, gone bool) {
	if w, ok := r.waits[hookID]; ok {
		w.gone = gone
		close(w.done)
		delete(r.waits, hookID)
	}
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
		t.closeWith("shutdown")
	}
}
