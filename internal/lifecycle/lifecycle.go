// Package lifecycle owns every transition between hooks and the tunnels that
// serve them: attaching, taking over, detaching, deleting and expiring.
package lifecycle

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
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
}

// Lifecycle tracks which tunnel serves each hook. A hook is served by at
// most one tunnel; attaching a newer tunnel replaces the older one
// ("takeover"). mu guards the registry state and detached-since; it is
// taken before the hook manager's lock, never after.
type Lifecycle struct {
	mu        sync.Mutex
	byHook    map[hooks.ID]*tunnel.Tunnel
	served    map[*tunnel.Tunnel][]hooks.ID
	waits     map[hooks.ID]*waiters
	detached  map[hooks.ID]time.Time // when each hook's last tunnel ended
	started   time.Time
	updateSeq uint64 // numbers hook-updated notices, see NotifyUpdated
	now       func() time.Time
	cfg       Config

	attachLocks sync.Map // tailcfg.UserID → *sync.Mutex, see Admit
}

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
// the hook under the same lock a delete takes, so a delete cannot slip
// between the two and strand the waiter.
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
	l.mu.Lock()
	ts := make([]*tunnel.Tunnel, 0, len(l.served))
	for t := range l.served {
		ts = append(ts, t)
	}
	l.mu.Unlock()
	for _, t := range ts {
		t.CloseWith("shutdown")
	}
}

// hookInfo describes h to a tunnel client.
func (l *Lifecycle) hookInfo(h hooks.Hook) tunnel.HookInfo {
	info := tunnel.HookInfo{ID: string(h.ID), URL: l.cfg.BaseURL + h.Route(), Public: h.Public}
	if h.Public && l.cfg.PublicBaseURL != "" {
		info.PublicURL = l.cfg.PublicBaseURL + h.PublicRoute()
	}
	return info
}
