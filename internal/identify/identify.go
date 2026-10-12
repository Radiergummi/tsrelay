// Package identify resolves the tailnet identity and relay permissions of a peer.
package identify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg/peercap"
)

// Identifier resolves the identity behind a connection's remote address.
type Identifier interface {
	Identify(ctx context.Context, remoteAddr string) (authz.Identity, error)
}

// WhoIser is the subset of *local.Client used by Tailscale.
type WhoIser interface {
	WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
}

// Tailscale identifies peers with the LocalAPI WhoIs call.
type Tailscale struct {
	Client   WhoIser
	Cap      peercap.Cap
	Defaults authz.Limits
	// Logger receives cap.parse_error events, once per node and error.
	Logger *slog.Logger

	seen sync.Map
}

func (t *Tailscale) Identify(ctx context.Context, remoteAddr string) (authz.Identity, error) {
	res, err := t.Client.WhoIs(ctx, remoteAddr)
	if err != nil {
		return authz.Identity{}, fmt.Errorf("whois %s: %w", remoteAddr, err)
	}
	id, errs := FromWhoIs(res, t.Cap, t.Defaults)
	for _, e := range errs {
		key := string(id.NodeID) + "\x00" + e.Error()
		if _, dup := t.seen.LoadOrStore(key, struct{}{}); !dup {
			t.Logger.Warn("cap.parse_error", "node", id.NodeName, "error", e.Error())
		}
	}
	return id, nil
}

// FromWhoIs converts a WhoIs response into an Identity. Tagged nodes carry no
// user, because WhoIs reports the generic "tagged-devices" profile for them.
func FromWhoIs(res *apitype.WhoIsResponse, c peercap.Cap, defaults authz.Limits) (authz.Identity, []error) {
	var id authz.Identity
	if n := res.Node; n != nil {
		id.NodeID = n.StableID
		id.NodeName, _, _ = strings.Cut(strings.TrimSuffix(n.Name, "."), ".")
		id.Tags = n.Tags
		if !n.IsTagged() && res.UserProfile != nil {
			id.UserID = res.UserProfile.ID
			id.Login = res.UserProfile.LoginName
		}
	}
	var errs []error
	id.Grants, errs = authz.Parse(res.CapMap[c], defaults)
	return id, errs
}
