package lifecycle

import (
	"slices"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"tailscale.com/tailcfg"
)

// replacement reports hooks a tunnel lost to a newer tunnel. An orphaned
// tunnel serves no hooks any more and should be closed after it was told.
type replacement struct {
	tunnel   *tunnel.Tunnel
	hooks    []hooks.ID
	orphaned bool
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

// The helpers below require l.mu.

// attachLocked makes t serve ids, taking them over from other tunnels, and
// wakes the calls waiting for them.
func (l *Lifecycle) attachLocked(t *tunnel.Tunnel, ids []hooks.ID) []replacement {
	lost := map[*tunnel.Tunnel][]hooks.ID{}
	for _, id := range ids {
		l.wakeLocked(id, false)
		if old, ok := l.byHook[id]; ok && old != t {
			lost[old] = append(lost[old], id)
			l.served[old] = slices.DeleteFunc(l.served[old], func(x hooks.ID) bool { return x == id })
		}
		l.byHook[id] = t
		if !slices.Contains(l.served[t], id) {
			l.served[t] = append(l.served[t], id)
		}
	}
	var out []replacement
	for old, hs := range lost {
		orphaned := len(l.served[old]) == 0
		if orphaned {
			delete(l.served, old)
		}
		out = append(out, replacement{tunnel: old, hooks: hs, orphaned: orphaned})
	}
	return out
}

// detachLocked removes t and returns the hooks it was still serving.
func (l *Lifecycle) detachLocked(t *tunnel.Tunnel) []hooks.ID {
	hs := l.served[t]
	delete(l.served, t)
	for _, id := range hs {
		if l.byHook[id] == t {
			delete(l.byHook, id)
		}
	}
	return hs
}

// detachHookLocked stops serving id, e.g. because the hook was deleted.
func (l *Lifecycle) detachHookLocked(id hooks.ID) (t *tunnel.Tunnel, orphaned, ok bool) {
	t, ok = l.byHook[id]
	if !ok {
		return nil, false, false
	}
	delete(l.byHook, id)
	l.served[t] = slices.DeleteFunc(l.served[t], func(x hooks.ID) bool { return x == id })
	if len(l.served[t]) == 0 {
		delete(l.served, t)
		orphaned = true
	}
	return t, orphaned, true
}

// detachFromLocked stops t serving those of ids it still serves, and returns
// them. Hooks another tunnel has taken over meanwhile stay with that
// tunnel. orphaned reports whether t now serves no hooks.
func (l *Lifecycle) detachFromLocked(t *tunnel.Tunnel, ids []hooks.ID) (detached []hooks.ID, orphaned bool) {
	for _, id := range ids {
		if l.byHook[id] != t {
			continue
		}
		delete(l.byHook, id)
		l.served[t] = slices.DeleteFunc(l.served[t], func(x hooks.ID) bool { return x == id })
		detached = append(detached, id)
	}
	if len(detached) > 0 && len(l.served[t]) == 0 {
		delete(l.served, t)
		orphaned = true
	}
	return detached, orphaned
}

// countOwnerLocked returns how many tunnels of user serve hooks. A tunnel
// whose hooks are all among taking is left out: attaching those hooks to a
// new tunnel takes them over and orphans it.
func (l *Lifecycle) countOwnerLocked(user tailcfg.UserID, taking []hooks.ID) int {
	n := 0
	for t, hs := range l.served {
		keeps := slices.ContainsFunc(hs, func(id hooks.ID) bool { return !slices.Contains(taking, id) })
		if t.Owner.UserID == user && keeps {
			n++
		}
	}
	return n
}

// wakeLocked wakes the calls waiting for id; gone fails them.
func (l *Lifecycle) wakeLocked(id hooks.ID, gone bool) {
	if w, ok := l.waits[id]; ok {
		w.gone = gone
		close(w.done)
		delete(l.waits, id)
	}
}

// leave unregisters one waiter of w, and removes w once nobody waits on it.
// An entry that was woken has already been removed or replaced.
func (l *Lifecycle) leave(id hooks.ID, w *waiters) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if w.n--; w.n == 0 && l.waits[id] == w {
		delete(l.waits, id)
	}
}
