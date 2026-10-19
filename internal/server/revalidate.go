package server

import (
	"context"
	"time"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// identifyTimeout bounds one WhoIs during revalidation.
const identifyTimeout = 10 * time.Second

// maxIdentifyFailures is how many revalidations in a row may fail to identify
// a tunnel's node before its hooks are revoked.
const maxIdentifyFailures = 3

// RunRevalidator re-authorizes every attached tunnel every interval until
// ctx is done (DESIGN.md §5.5).
func (s *Server) RunRevalidator(ctx context.Context, interval time.Duration) {
	runEvery(ctx, interval, func() { s.revalidate(ctx) })
}

// revalidate identifies each tunnel's client again and revokes the hooks
// its owner may no longer serve.
func (s *Server) revalidate(ctx context.Context) {
	snapshot := s.tunnels.Snapshot()
	for t := range s.identifyFailures {
		if _, ok := snapshot[t]; !ok {
			delete(s.identifyFailures, t)
		}
	}
	for t, hookIDs := range snapshot {
		if ctx.Err() != nil {
			return
		}
		if revoked, reason := s.check(ctx, t, hookIDs); len(revoked) > 0 {
			s.tunnels.Revoke(ctx, t, revoked, reason)
		}
	}
}

// check returns those of hookIDs that t's owner may no longer serve, and why.
func (s *Server) check(ctx context.Context, t *tunnel.Tunnel, hookIDs []hooks.ID) ([]hooks.ID, string) {
	ictx, cancel := context.WithTimeout(ctx, identifyTimeout)
	defer cancel()
	id, err := s.identifier.Identify(ictx, t.RemoteAddr)
	if err == nil {
		delete(s.identifyFailures, t)
	}
	switch {
	case err != nil && ctx.Err() != nil:
		return nil, "" // shutting down; nothing was revoked
	case err != nil:
		// WhoIs can fail transiently; revoke only after repeated failures.
		if s.identifyFailures == nil {
			s.identifyFailures = map[*tunnel.Tunnel]int{}
		}
		s.identifyFailures[t]++
		if s.identifyFailures[t] < maxIdentifyFailures {
			return nil, ""
		}
		delete(s.identifyFailures, t)
		return hookIDs, "the node could not be identified"
	case !id.IsUser():
		return hookIDs, "the node no longer belongs to a user"
	case id.UserID != t.Owner.UserID:
		return hookIDs, "the node belongs to another user now"
	case !id.Grants.Has(authz.RoleReceiver):
		return hookIDs, "the receiver role was revoked"
	}
	t.SetPublic(len(id.Grants.PublicPatterns) > 0)
	var revoked []hooks.ID
	for _, hid := range hookIDs {
		// A deleted hook is skipped: its deletion detaches it.
		if h, ok := s.hooks.Get(hid); ok && !id.Grants.Allows(authz.RoleReceiver, h.Channel) {
			revoked = append(revoked, hid)
		}
	}
	return revoked, "the grants no longer cover the hook's channel"
}
