package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// identifyTimeout bounds one WhoIs during revalidation.
const identifyTimeout = 10 * time.Second

// RunRevalidator re-authorizes every attached tunnel every interval until
// ctx is done (DESIGN.md §5.5).
func (s *Server) RunRevalidator(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.revalidate(ctx)
	}
}

// revalidate identifies each tunnel's client again and revokes the hooks
// its owner may no longer serve.
func (s *Server) revalidate(ctx context.Context) {
	for t, hookIDs := range s.tunnels.Snapshot() {
		if ctx.Err() != nil {
			return
		}
		if revoked, reason := s.check(ctx, t, hookIDs); len(revoked) > 0 {
			s.revoke(ctx, t, revoked, reason)
		}
	}
}

// check returns those of hookIDs that t's owner may no longer serve, and why.
func (s *Server) check(ctx context.Context, t *tunnel.Tunnel, hookIDs []string) ([]string, string) {
	ictx, cancel := context.WithTimeout(ctx, identifyTimeout)
	defer cancel()
	id, err := s.identifier.Identify(ictx, t.RemoteAddr)
	switch {
	case err != nil && ctx.Err() != nil:
		return nil, "" // shutting down; nothing was revoked
	case err != nil:
		return hookIDs, "the node could not be identified"
	case !id.IsUser():
		return hookIDs, "the node no longer belongs to a user"
	case id.UserID != t.Owner.UserID:
		return hookIDs, "the node belongs to another user now"
	case !id.Grants.Has(authz.RoleReceiver):
		return hookIDs, "the receiver role was revoked"
	}
	var revoked []string
	for _, hid := range hookIDs {
		// A deleted hook is skipped: its deletion detaches it.
		if h, ok := s.hooks.Get(hooks.ID(hid)); ok && !id.Grants.Allows(authz.RoleReceiver, h.Channel) {
			revoked = append(revoked, hid)
		}
	}
	return revoked, "the grants no longer cover the hook's channel"
}

// revoke detaches hookIDs from t, tells its client, and closes t if it
// serves nothing else. Only hooks t still serves are detached: one that a
// newer tunnel took over since the check stays with that tunnel.
func (s *Server) revoke(ctx context.Context, t *tunnel.Tunnel, hookIDs []string, reason string) {
	detached, orphaned := s.tunnels.DetachFrom(t, hookIDs)
	if len(detached) == 0 {
		return
	}
	infos := make([]tunnel.HookInfo, len(detached))
	for i, hid := range detached {
		s.hooks.SetAttached(hooks.ID(hid), false)
		infos[i] = tunnel.HookInfo{ID: hid}
	}
	s.audit.Log(ctx, audit.TunnelRevoked, audit.Actor(t.Owner), slog.String("tunnel", t.ID),
		slog.Any("hooks", detached), slog.String("reason", reason))
	t.Notify(tunnel.Control{Type: tunnel.ControlRevoked, Hooks: infos}, orphaned)
}
