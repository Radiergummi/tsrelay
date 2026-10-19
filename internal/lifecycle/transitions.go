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
	l.announce.Lock()
	defer l.announce.Unlock()
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
	l.cfg.Events.Publish(events.HookChanged(h.Owner, h.Channel, string(h.ID)))
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
		l.publishTunnelChange(t.Owner.UserID, id)
	}
	l.cfg.Audit.Log(ctx, audit.TunnelRevoked, audit.Actor(t.Owner), slog.String("tunnel", t.ID),
		slog.Any("hooks", detached), slog.String("reason", reason))
	t.Notify(tunnel.Control{Type: tunnel.ControlRevoked, Hooks: idInfos(detached)}, orphaned)
}

// Create creates a hook for actor, as hooks.Manager.Create does, and
// announces it in the audit log and as a hook event.
func (l *Lifecycle) Create(ctx context.Context, actor authz.Identity, req hooks.CreateRequest) (hooks.Hook, error) {
	l.announce.Lock()
	defer l.announce.Unlock()
	h, err := l.cfg.Hooks.Create(ctx, actor, req)
	if err != nil {
		return h, err
	}
	l.cfg.Audit.Log(ctx, audit.HookCreate, audit.Actor(actor), audit.Hook(h), slog.Bool("ephemeral", h.Ephemeral))
	l.cfg.Events.Publish(events.HookChanged(h.Owner, h.Channel, string(h.ID)))
	return h, nil
}

// Update applies req to the hook id on behalf of actor, as
// hooks.Manager.Update does, and announces the change: in the audit log, as
// a hook event, and to the tunnel serving the hook. An update that changes
// no field is not announced.
func (l *Lifecycle) Update(ctx context.Context, actor authz.Identity, id hooks.ID, req hooks.UpdateRequest) (hooks.Hook, []string, error) {
	l.announce.Lock()
	defer l.announce.Unlock()
	h, changed, err := l.cfg.Hooks.Update(ctx, actor, id, req)
	if err != nil || len(changed) == 0 {
		return h, changed, err
	}
	l.updated(ctx, actor, h, changed)
	return h, changed, nil
}

// RotatePublicToken gives the public hook id a new token on behalf of
// actor, as hooks.Manager.RotatePublicToken does, and announces the change
// as Update does.
func (l *Lifecycle) RotatePublicToken(ctx context.Context, actor authz.Identity, id hooks.ID) (hooks.Hook, error) {
	l.announce.Lock()
	defer l.announce.Unlock()
	h, err := l.cfg.Hooks.RotatePublicToken(ctx, actor, id)
	if err != nil {
		return h, err
	}
	l.updated(ctx, actor, h, []string{"publicUrl"})
	return h, nil
}

func (l *Lifecycle) updated(ctx context.Context, actor authz.Identity, h hooks.Hook, changed []string) {
	l.cfg.Audit.Log(ctx, audit.HookUpdate, audit.Actor(actor), audit.Hook(h), slog.Any("changed", changed))
	l.cfg.Events.Publish(events.HookChanged(h.Owner, h.Channel, string(h.ID)))
	l.notifyUpdated(h.ID)
}

// notifyUpdated tells the tunnel serving the hook its current URL. Messages
// are sent in the background and can overtake each other, so each carries
// a sequence number. The number and the hook are taken under one lock, so a
// higher number never carries an older URL.
func (l *Lifecycle) notifyUpdated(id hooks.ID) {
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
