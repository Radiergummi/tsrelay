package lifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/hooks"
)

// Reap removes every expired hook, and every ephemeral hook that has been
// without a tunnel for at least grace. Expired hooks go even while served.
// A hook's grace starts when its last tunnel ended; one that never had a
// tunnel, or lost it before this relay started, starts at the later of its
// creation and the relay's start, so a restart gives it a full grace
// period. A store error stops the pass and is returned; hooks removed
// before it keep their effects.
func (l *Lifecycle) Reap(ctx context.Context, grace time.Duration) error {
	l.announce.Lock()
	defer l.announce.Unlock()
	now := l.now()
	// List outside l.mu, so callbacks don't wait on the copy and sort.
	var candidates []hooks.ID
	for _, h := range l.cfg.Hooks.ListAll() {
		if h.Ephemeral || h.Expired(now) {
			candidates = append(candidates, h.ID)
		}
	}

	var removed []removal
	var err error
	// Decide on the stored hook, not on the listing: a tunnel may have
	// attached since. l.mu is not held across the store delete, which may be
	// slow, so a tunnel can still attach before the hook is forgotten; the
	// forgetting detaches it again.
	reapable := func(h hooks.Hook) bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return h.Expired(now) || l.idleLocked(h, now, grace)
	}
	for _, id := range candidates {
		var cur hooks.Hook
		var ok bool
		cur, ok, err = l.cfg.Hooks.DeleteIf(ctx, id, reapable)
		if errors.Is(err, hooks.ErrNotFound) {
			err = nil // deleted since the listing
			continue
		}
		if err != nil {
			break
		}
		if ok {
			l.mu.Lock()
			removed = append(removed, l.forgetLocked(cur))
			l.mu.Unlock()
		}
	}

	for _, r := range removed {
		l.finishRemoval(ctx, r, audit.HookExpire)
	}
	return err
}

// idleLocked reports whether h is ephemeral and has had no tunnel for grace.
func (l *Lifecycle) idleLocked(h hooks.Hook, now time.Time, grace time.Duration) bool {
	if !h.Ephemeral {
		return false
	}
	if _, served := l.byHook[h.ID]; served {
		return false
	}
	since, ok := l.detached[h.ID]
	if !ok {
		since = h.CreatedAt
		if l.started.After(since) {
			since = l.started
		}
	}
	return now.Sub(since) >= grace
}
