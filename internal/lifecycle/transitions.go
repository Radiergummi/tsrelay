package lifecycle

import (
	"context"
	"errors"
	"log/slog"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/events"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// Delete deletes h on behalf of actor, fails the calls waiting for it,
// tells the tunnel serving it, and forgets its captured calls. A hook a
// concurrent delete already removed is not an error. The store deletes
// under l.mu, so a Wait cannot register for a hook that is already gone.
func (l *Lifecycle) Delete(ctx context.Context, actor authz.Identity, h hooks.Hook) error {
	l.mu.Lock()
	if err := l.cfg.Hooks.Delete(ctx, h.ID); err != nil && !errors.Is(err, hooks.ErrNotFound) {
		l.mu.Unlock()
		return err
	}
	l.wakeLocked(h.ID, true)
	t, orphaned, attached := l.detachHookLocked(h.ID)
	delete(l.detached, h.ID)
	l.mu.Unlock()

	if attached {
		t.Notify(tunnel.Control{Type: tunnel.ControlHookDeleted, Hooks: []tunnel.HookInfo{{ID: string(h.ID)}}}, orphaned)
	}
	if l.cfg.Inspect != nil {
		l.cfg.Inspect.Forget(h.ID)
	}
	l.cfg.Audit.Log(ctx, audit.HookDelete, audit.Actor(actor), audit.Hook(h))
	l.cfg.Events.Publish(events.HookChanged(h.Owner, string(h.ID)))
	return nil
}

// Revoke detaches ids from t, tells its client, and closes t if it serves
// nothing else. Only hooks t still serves are detached: one that a newer
// tunnel took over since the check stays with that tunnel.
func (l *Lifecycle) Revoke(ctx context.Context, t *tunnel.Tunnel, ids []hooks.ID, reason string) {
	l.mu.Lock()
	detached, orphaned := l.detachFromLocked(t, ids)
	now := l.now()
	for _, id := range detached {
		l.detached[id] = now
	}
	l.mu.Unlock()
	if len(detached) == 0 {
		return
	}
	infos := make([]tunnel.HookInfo, len(detached))
	for i, id := range detached {
		l.cfg.Events.Publish(events.HookChanged(t.Owner.UserID, string(id)))
		infos[i] = tunnel.HookInfo{ID: string(id)}
	}
	l.cfg.Audit.Log(ctx, audit.TunnelRevoked, audit.Actor(t.Owner), slog.String("tunnel", t.ID),
		slog.Any("hooks", detached), slog.String("reason", reason))
	t.Notify(tunnel.Control{Type: tunnel.ControlRevoked, Hooks: infos}, orphaned)
}

// NotifyUpdated tells the tunnel serving the hook its current URL. Messages
// are sent in the background and can overtake each other, so each carries
// a sequence number. The number and the hook are taken under one lock, so a
// higher number never carries an older URL.
func (l *Lifecycle) NotifyUpdated(id hooks.ID) {
	l.mu.Lock()
	h, ok := l.cfg.Hooks.Get(id)
	l.updateSeq++
	seq := l.updateSeq
	t, serving := l.byHook[id]
	l.mu.Unlock()
	if !ok || !serving {
		return // deleted meanwhile (the delete sends hook-deleted), or nobody listens
	}
	t.Notify(tunnel.Control{Type: tunnel.ControlHookUpdated, Seq: seq, Hooks: []tunnel.HookInfo{l.hookInfo(h)}}, false)
}
