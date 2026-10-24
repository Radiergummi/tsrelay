package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Radiergummi/tsrelay/internal/access"
	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/events"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"tailscale.com/tailcfg"
)

// AdmitErrorKind says which attach check failed.
type AdmitErrorKind int

const (
	HookNotFound   AdmitErrorKind = iota // a hook is missing or not the owner's
	ChannelDenied                        // the owner's grants don't cover the hook's channel
	Superseded                           // a resume, but another session serves the hook
	Rejected                             // the hook's attach policy refuses a takeover
	TooManyTunnels                       // the owner is at maxTunnels
)

// AdmitError is why Admit refused an attach. Its message is the one the
// client sees.
type AdmitError struct {
	Kind        AdmitErrorKind
	Hook        hooks.ID
	Channel     string
	Have, Limit int
}

func (e *AdmitError) Error() string {
	switch e.Kind {
	case HookNotFound:
		return fmt.Sprintf("hook %s not found", e.Hook)
	case ChannelDenied:
		return fmt.Sprintf("your grants no longer cover channel %q", e.Channel)
	case Superseded:
		return fmt.Sprintf("hook %s is now served by another tunnel; start tsrelay again to take it over", e.Hook)
	case Rejected:
		return fmt.Sprintf("hook %s is served by another tunnel, and its attach policy is %q; stop that tunnel first", e.Hook, hooks.AttachReject)
	default:
		return fmt.Sprintf("you already have %d tunnels attached, the most your grants allow (maxTunnels %d); stop one first", e.Have, e.Limit)
	}
}

// Ticket is an admitted attach. It holds the owner's attach lock until
// Attach or Close.
type Ticket struct {
	l         *Lifecycle
	owner     authz.Identity
	requested []hooks.Hook
	unlock    func()
	once      sync.Once
}

// Admit checks that owner may attach a tunnel to ids. On success it keeps
// the owner's attach lock, which the returned Ticket releases: checks and
// attach must not interleave with the owner's other attaches, or two of
// them could both pass maxTunnels or "reject".
func (l *Lifecycle) Admit(ctx context.Context, owner authz.Identity, ids []hooks.ID, session string, resume bool) (*Ticket, *AdmitError) {
	v, _ := l.attachLocks.LoadOrStore(owner.UserID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	requested, ae := l.admit(owner, ids, session, resume)
	if ae != nil {
		mu.Unlock()
		if ae.Kind == TooManyTunnels {
			l.cfg.Audit.Log(ctx, audit.AuthzDeny, audit.Actor(owner), slog.String("action", "tunnel.attach"), slog.String("reason", "maxTunnels"))
		}
		return nil, ae
	}
	return &Ticket{l: l, owner: owner, requested: requested, unlock: mu.Unlock}, nil
}

// admit runs the attach checks in the order the API has always run them.
func (l *Lifecycle) admit(owner authz.Identity, ids []hooks.ID, session string, resume bool) ([]hooks.Hook, *AdmitError) {
	l.mu.Lock()
	defer l.mu.Unlock()
	requested := make([]hooks.Hook, 0, len(ids))
	for _, id := range ids {
		h, ok := l.cfg.Hooks.Get(id)
		if !ok {
			return nil, &AdmitError{Kind: HookNotFound, Hook: id}
		}
		switch err := access.Serve(owner, h.Access()); {
		case errors.Is(err, access.ErrNotUser), errors.Is(err, access.ErrOtherUser):
			return nil, &AdmitError{Kind: HookNotFound, Hook: id}
		case err != nil:
			return nil, &AdmitError{Kind: ChannelDenied, Hook: id, Channel: h.Channel}
		}
		if cur, ok := l.byHook[id]; ok {
			if resume && cur.Session != session {
				return nil, &AdmitError{Kind: Superseded, Hook: id}
			}
			// Under "reject", only the serving session may replace its own
			// (stale) tunnel.
			if l.cfg.Hooks.Effective(h).AttachPolicy == hooks.AttachReject && (session == "" || cur.Session != session) {
				return nil, &AdmitError{Kind: Rejected, Hook: id}
			}
		}
		requested = append(requested, h)
	}
	if n, limit := l.countOwnerLocked(owner.UserID, ids), owner.Grants.Limits.MaxTunnels; n >= limit {
		return nil, &AdmitError{Kind: TooManyTunnels, Have: n, Limit: limit}
	}
	return requested, nil
}

// Close releases the owner's attach lock without attaching, e.g. because
// the upgrade failed.
func (tk *Ticket) Close() { tk.once.Do(tk.unlock) }

// Attach makes t serve the admitted hooks, tells the client, releases the
// owner's attach lock, and detaches t again once it ends. onEnd, if set, is
// called after that with how long t was attached. ctx is used for the audit
// records, also after t ends.
func (tk *Ticket) Attach(ctx context.Context, t *tunnel.Tunnel, onEnd func(lifetime time.Duration)) {
	defer tk.Close()
	l := tk.l
	attachedAt := time.Now()

	// A hook deleted since Admit must not be attached, nor one the owner
	// may no longer serve because its channel changed. One that another
	// tunnel took over meanwhile is taken from it.
	var live, goneIDs, revoked []hooks.ID
	var reason string
	var infos []tunnel.HookInfo
	l.mu.Lock()
	for _, h := range tk.requested {
		cur, ok := l.cfg.Hooks.Get(h.ID)
		if !ok {
			// Delete and Reap forget a hook under l.mu once it left the store,
			// detaching any tunnel attached meanwhile, so it stays served by none.
			goneIDs = append(goneIDs, h.ID)
			continue
		}
		if err := access.Serve(tk.owner, cur.Access()); err != nil {
			revoked = append(revoked, h.ID)
			reason = RevokeReason(err)
			continue
		}
		live = append(live, h.ID)
		infos = append(infos, l.hookInfo(cur))
	}
	reps := l.attachLocked(t, live)
	// Numbered like hook-updated: an update made once t serves the hooks
	// gets a higher number, so a client that sees it first keeps it.
	l.updateSeq++
	seq := l.updateSeq
	l.mu.Unlock()

	for _, rep := range reps {
		l.cfg.Audit.Log(ctx, audit.TunnelReplaced, slog.String("tunnel", rep.tunnel.ID), slog.String("by", t.ID), slog.Any("hooks", rep.hooks))
		rep.tunnel.Notify(tunnel.Control{Type: tunnel.ControlReplaced, Hooks: idInfos(rep.hooks)}, rep.orphaned)
	}
	for _, id := range live {
		l.publishTunnelChange(tk.owner.UserID, id)
	}
	l.cfg.Audit.Log(ctx, audit.TunnelAttach, audit.Actor(tk.owner), slog.String("tunnel", t.ID), slog.Any("hooks", live))
	if len(infos) > 0 {
		t.Notify(tunnel.Control{Type: tunnel.ControlAttached, Seq: seq, Hooks: infos}, false)
	}
	if len(revoked) > 0 {
		l.cfg.Audit.Log(ctx, audit.TunnelRevoked, audit.Actor(tk.owner), slog.String("tunnel", t.ID),
			slog.Any("hooks", revoked), slog.String("reason", reason))
		t.Notify(tunnel.Control{Type: tunnel.ControlRevoked, Hooks: idInfos(revoked)}, len(live) == 0 && len(goneIDs) == 0)
	}
	if len(goneIDs) > 0 {
		t.Notify(tunnel.Control{Type: tunnel.ControlHookDeleted, Hooks: idInfos(goneIDs)}, len(live) == 0)
	}

	// Capture only what the goroutine needs, not the whole Ticket.
	owner := tk.owner
	go func() {
		<-t.Done()
		l.mu.Lock()
		hs := l.detachLocked(t)
		l.mu.Unlock()
		for _, id := range hs {
			l.publishTunnelChange(owner.UserID, id)
		}
		l.cfg.Audit.Log(ctx, audit.TunnelDetach, audit.Actor(owner), slog.String("tunnel", t.ID))
		if onEnd != nil {
			onEnd(time.Since(attachedAt))
		}
	}()
}

// publishTunnelChange announces that the hook id's tunnel attached or
// detached. A hook deleted meanwhile has no channel any more; its deletion
// was announced with one.
func (l *Lifecycle) publishTunnelChange(owner tailcfg.UserID, id hooks.ID) {
	h, _ := l.cfg.Hooks.Get(id)
	l.cfg.Events.Publish(events.HookChanged(owner, h.Channel, string(id)))
}
