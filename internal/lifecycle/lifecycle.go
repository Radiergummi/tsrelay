// Package lifecycle owns every transition between hooks and the tunnels that
// serve them: create, attach (with takeover), detach when a tunnel ends, delete,
// expire (Reap), revoke, update, and shutdown. It is the
// only writer of which tunnel serves which hook and since when a hook has
// had none.
package lifecycle

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/events"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// ErrHookGone is returned by Wait when the hook was deleted while waiting.
var ErrHookGone = errors.New("lifecycle: the hook was deleted")

// Config holds what a Lifecycle works with.
type Config struct {
	Hooks         *hooks.Manager
	Inspect       *inspect.Inspector // nil: nothing to forget
	Events        *events.Hub        // nil-safe already
	Audit         *audit.Logger
	BaseURL       string // HookInfo.URL = BaseURL + h.Route()
	PublicBaseURL string // HookInfo.PublicURL; empty: the relay serves no Funnel
	// Identify identifies a tunnel's node again, by its remote address, when
	// a hook it serves changes. nil: its last known grants decide.
	Identify func(ctx context.Context, remoteAddr string) (authz.Identity, error)
}

// Lifecycle tracks which tunnel serves each hook. A hook is served by at
// most one tunnel; attaching a newer tunnel replaces the older one
// ("takeover"). mu guards the registry state and detached-since.
//
// Locks are taken in the order announce → owner attach lock → the hook
// store's write lock → mu → hooks.Manager.mu; nothing is sent, logged or
// published while mu is held, and mu is never held across a store write.
// announce is held across a hook's change in the store and its
// announcement, so changes are announced in the order they were made. Under
// mu:
//   - byHook only names stored hooks, except for a hook just deleted from
//     the store and not yet forgotten (see forgetLocked);
//   - detached only names stored hooks that no tunnel serves.
type Lifecycle struct {
	announce  sync.Mutex // see above; held by Create, Update, RotatePublicToken, Delete and Reap
	mu        sync.Mutex
	byHook    map[hooks.ID]*tunnel.Tunnel
	served    map[*tunnel.Tunnel][]hooks.ID
	waits     map[hooks.ID]*waiters
	detached  map[hooks.ID]time.Time // when each hook's last tunnel ended
	started   time.Time
	updateSeq uint64 // numbers hook-updated notices, see notifyUpdated
	now       func() time.Time
	cfg       Config

	attachLocks sync.Map // tailcfg.UserID → *sync.Mutex, see Admit
}

// New returns a Lifecycle with no tunnels attached. Ephemeral hooks get
// their grace period counted from now at the earliest, see Reap.
func New(cfg Config) *Lifecycle {
	l := &Lifecycle{
		byHook:   map[hooks.ID]*tunnel.Tunnel{},
		served:   map[*tunnel.Tunnel][]hooks.ID{},
		waits:    map[hooks.ID]*waiters{},
		detached: map[hooks.ID]time.Time{},
		now:      time.Now,
		cfg:      cfg,
	}
	l.started = l.now()
	return l
}

// Lookup returns the tunnel serving id.
func (l *Lifecycle) Lookup(id hooks.ID) (*tunnel.Tunnel, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.byHook[id]
	return t, ok
}

// Wait returns the tunnel serving id, waiting for one to attach if
// necessary. Only an attach for id itself wakes it. It fails with
// ErrHookGone if the hook does not exist, or is deleted while waiting, and
// with the context's cause when ctx is done. The wait registers and checks
// the hook under l.mu, and a delete forgets the hook under l.mu after it
// left the store, so a delete cannot slip between the two and strand the
// waiter.
func (l *Lifecycle) Wait(ctx context.Context, id hooks.ID) (*tunnel.Tunnel, error) {
	for {
		l.mu.Lock()
		if t, ok := l.byHook[id]; ok {
			l.mu.Unlock()
			return t, nil
		}
		if _, ok := l.cfg.Hooks.Get(id); !ok {
			l.mu.Unlock()
			return nil, ErrHookGone
		}
		w := l.waits[id]
		if w == nil {
			w = &waiters{done: make(chan struct{})}
			l.waits[id] = w
		}
		w.n++
		l.mu.Unlock()
		select {
		case <-w.done:
			l.leave(id, w)
			if w.gone {
				return nil, ErrHookGone
			}
			// A tunnel attached; it may have gone again before we looked.
		case <-ctx.Done():
			l.leave(id, w)
			return nil, context.Cause(ctx)
		}
	}
}

// Snapshot returns every attached tunnel with the hooks it serves.
func (l *Lifecycle) Snapshot() map[*tunnel.Tunnel][]hooks.ID {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[*tunnel.Tunnel][]hooks.ID, len(l.served))
	for t, ids := range l.served {
		out[t] = append([]hooks.ID(nil), ids...)
	}
	return out
}

// Close closes every attached tunnel, for relay shutdown.
func (l *Lifecycle) Close() {
	for t := range l.Snapshot() {
		t.CloseWith("shutdown")
	}
}

// idInfos names ids to a tunnel client, for notices that need no more.
func idInfos(ids []hooks.ID) []tunnel.HookInfo {
	infos := make([]tunnel.HookInfo, len(ids))
	for i, id := range ids {
		infos[i] = tunnel.HookInfo{ID: string(id)}
	}
	return infos
}

// hookInfo describes h to a tunnel client.
func (l *Lifecycle) hookInfo(h hooks.Hook) tunnel.HookInfo {
	return tunnel.HookInfo{ID: string(h.ID), URL: l.cfg.BaseURL + h.Route(), Path: h.Path, Public: h.Public, PublicURL: h.PublicURL(l.cfg.PublicBaseURL)}
}
