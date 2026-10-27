package lifecycle

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"github.com/Radiergummi/tsrelay/internal/access"
	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/events"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// Delete deletes the hook id on behalf of actor, fails the calls waiting
// for it, tells the tunnel serving it, and forgets its captured calls. A
// hook a concurrent delete or the reaper already removed is not an error,
// and that removal has already had these effects. Whether actor may delete
// the hook is decided on the stored hook, under the store's lock: one that
// moved out of actor's sight since the caller's check is hooks.ErrNotFound.
func (l *Lifecycle) Delete(ctx context.Context, actor authz.Identity, id hooks.ID) error {
	l.announce.Lock()
	defer l.announce.Unlock()
	h, deleted, err := l.cfg.Hooks.DeleteIf(ctx, id, func(h hooks.Hook) bool {
		return access.Delete(actor, h.Access()) == nil
	})
	var r removal
	if deleted {
		l.mu.Lock()
		r = l.forgetLocked(h)
		l.mu.Unlock()
	}
	switch {
	case errors.Is(err, hooks.ErrNotFound):
		return nil
	case err != nil:
		return err
	case !deleted:
		return hooks.ErrNotFound
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

// CheckCreate validates req as Create would, without creating or
// announcing anything: the API's dry run.
func (l *Lifecycle) CheckCreate(ctx context.Context, actor authz.Identity, req hooks.CreateRequest) (hooks.Hook, error) {
	return l.cfg.Hooks.CheckCreate(ctx, actor, req)
}

// CheckUpdate validates req as Update would, without changing or
// announcing anything: the API's dry run.
func (l *Lifecycle) CheckUpdate(ctx context.Context, actor authz.Identity, id hooks.ID, req hooks.UpdateRequest) (hooks.Hook, []string, error) {
	return l.cfg.Hooks.CheckUpdate(ctx, actor, id, req)
}

// Update applies req to the hook id on behalf of actor, as
// hooks.Manager.Update does, and announces the change: in the audit log, as
// a hook event, and to the tunnel serving the hook. An update that changes
// no field is not announced. A change of the channel or public flag is
// checked against the serving tunnel's node, see recheck.
func (l *Lifecycle) Update(ctx context.Context, actor authz.Identity, id hooks.ID, req hooks.UpdateRequest) (hooks.Hook, []string, error) {
	h, changed, err := l.update(ctx, actor, id, req)
	if err == nil && slices.ContainsFunc(changed, func(f string) bool { return f == "channel" || f == "public" }) {
		l.recheck(ctx, id)
	}
	return h, changed, err
}

func (l *Lifecycle) update(ctx context.Context, actor authz.Identity, id hooks.ID, req hooks.UpdateRequest) (hooks.Hook, []string, error) {
	l.announce.Lock()
	defer l.announce.Unlock()
	old, _ := l.cfg.Hooks.Get(id) // every update holds announce, so this is the hook updated
	h, changed, err := l.cfg.Hooks.Update(ctx, actor, id, req)
	if err != nil || len(changed) == 0 {
		return h, changed, err
	}
	l.updated(ctx, actor, h, changed)
	if old.Channel != h.Channel {
		// Viewers of the old channel only, such as an admin scoped to it,
		// must learn that the hook left it.
		l.cfg.Events.Publish(events.HookChanged(h.Owner, old.Channel, string(h.ID)))
	}
	return h, changed, nil
}

// RotatePublicToken gives the public hook id a new token on behalf of
// actor, as hooks.Manager.RotatePublicToken does, and announces and checks
// the change as Update does.
func (l *Lifecycle) RotatePublicToken(ctx context.Context, actor authz.Identity, id hooks.ID) (hooks.Hook, error) {
	h, err := l.rotatePublicToken(ctx, actor, id)
	if err == nil {
		l.recheck(ctx, id)
	}
	return h, err
}

func (l *Lifecycle) rotatePublicToken(ctx context.Context, actor authz.Identity, id hooks.ID) (hooks.Hook, error) {
	l.announce.Lock()
	defer l.announce.Unlock()
	h, err := l.cfg.Hooks.RotatePublicToken(ctx, actor, id)
	if err != nil {
		return h, err
	}
	l.updated(ctx, actor, h, []string{"publicUrl"})
	return h, nil
}

// recheck identifies the node of the tunnel serving id again, records its
// grants, and revokes the hook if the node may no longer serve it. The
// change may have been made from another node, so the tunnel's own node
// decides. Losing the right to serve the hook on the internet only stops
// public calls (the relay checks that per call); it never revokes. If the
// node cannot be identified, its last known grants decide. It runs with no
// lock held: WhoIs may be slow.
func (l *Lifecycle) recheck(ctx context.Context, id hooks.ID) {
	t, ok := l.Lookup(id)
	if !ok {
		return
	}
	node := t.Identity()
	if l.cfg.Identify != nil {
		if got, err := l.cfg.Identify(ctx, t.RemoteAddr); err == nil {
			node = got
			l.RecordGrants(t, node)
		}
	}
	if revoked, reason := l.Reauthorize(t, node, []hooks.ID{id}); len(revoked) > 0 {
		l.Revoke(ctx, t, revoked, reason)
	}
}

// RecordGrants records the grants of node, t's node as just identified, on
// t if node still belongs to t's owner. Only a fresh identify may record
// them: t.Identity() is a snapshot that a concurrent identify may outdate.
func (l *Lifecycle) RecordGrants(t *tunnel.Tunnel, node authz.Identity) {
	if node.IsUser() && node.UserID == t.Owner.UserID {
		t.SetGrants(node.Grants)
	}
}

// Reauthorize checks ids, served by t, against node and returns those of
// ids node may no longer serve, and why. A deleted hook is skipped: its
// deletion detaches it. The caller revokes.
func (l *Lifecycle) Reauthorize(t *tunnel.Tunnel, node authz.Identity, ids []hooks.ID) (revoked []hooks.ID, reason string) {
	for _, id := range ids {
		h, ok := l.cfg.Hooks.Get(id)
		if !ok {
			continue
		}
		if err := access.Serve(node, h.Access()); err != nil {
			revoked = append(revoked, id)
			reason = revokeReason(err)
		}
	}
	return revoked, reason
}

// revokeReason explains an access.Serve refusal in a revocation.
func revokeReason(err error) string {
	switch {
	case errors.Is(err, access.ErrNotUser):
		return "the node no longer belongs to a user"
	case errors.Is(err, access.ErrOtherUser):
		return "the node belongs to another user now"
	case errors.Is(err, access.ErrNoReceiver):
		return "the receiver role was revoked"
	default:
		return "the grants no longer cover the hook's channel"
	}
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
