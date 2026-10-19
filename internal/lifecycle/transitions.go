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

// Delete deletes the hook id on behalf of actor, fails the calls waiting
// for it, tells the tunnel serving it, and forgets its captured calls. A
// hook a concurrent delete or the reaper already removed is not an error,
// and that removal has already had these effects. The store deletes under
// l.mu, so a Wait cannot register for a hook that is already gone.
func (l *Lifecycle) Delete(ctx context.Context, actor authz.Identity, id hooks.ID) error {
	l.mu.Lock()
	h, _, err := l.cfg.Hooks.DeleteIf(ctx, id, func(hooks.Hook) bool { return true })
	var r removal
	if err == nil {
		r = l.forgetLocked(h)
	}
	l.mu.Unlock()
	if errors.Is(err, hooks.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	l.finishRemoval(ctx, r, audit.HookDelete, audit.Actor(actor))
	return nil
}

// removal is what removing a hook leaves to do once l.mu is released.
type removal struct {
	hook     hooks.Hook
	tunnel   *tunnel.Tunnel // the tunnel that served the hook, if any
	orphaned bool           // tunnel serves nothing else now
}

// forgetLocked fails the calls waiting for the removed hook h and detaches
// it.
func (l *Lifecycle) forgetLocked(h hooks.Hook) removal {
	l.wakeLocked(h.ID, true)
	t, orphaned, _ := l.detachHookLocked(h.ID)
	delete(l.detached, h.ID)
	return removal{hook: h, tunnel: t, orphaned: orphaned}
}

// finishRemoval tells the tunnel that served the hook, forgets its captured
// calls, and records the removal under the audit message msg.
func (l *Lifecycle) finishRemoval(ctx context.Context, r removal, msg string, attrs ...slog.Attr) {
	h := r.hook
	if r.tunnel != nil {
		r.tunnel.Notify(tunnel.Control{Type: tunnel.ControlHookDeleted, Hooks: idInfos([]hooks.ID{h.ID})}, r.orphaned)
	}
	if l.cfg.Inspect != nil {
		l.cfg.Inspect.Forget(h.ID)
	}
	l.cfg.Audit.Log(ctx, msg, append(attrs, audit.Hook(h))...)
	l.cfg.Events.Publish(events.HookChanged(h.Owner, string(h.ID)))
}

// Revoke detaches ids from t, tells its client, and closes t if it serves
// nothing else. Only hooks t still serves are detached: one that a newer
// tunnel took over since the check stays with that tunnel.
func (l *Lifecycle) Revoke(ctx context.Context, t *tunnel.Tunnel, ids []hooks.ID, reason string) {
	l.mu.Lock()
	detached, orphaned := l.detachFromLocked(t, ids)
	l.mu.Unlock()
	if len(detached) == 0 {
		return
	}
	for _, id := range detached {
		l.cfg.Events.Publish(events.HookChanged(t.Owner.UserID, string(id)))
	}
	l.cfg.Audit.Log(ctx, audit.TunnelRevoked, audit.Actor(t.Owner), slog.String("tunnel", t.ID),
		slog.Any("hooks", detached), slog.String("reason", reason))
	t.Notify(tunnel.Control{Type: tunnel.ControlRevoked, Hooks: idInfos(detached)}, orphaned)
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
