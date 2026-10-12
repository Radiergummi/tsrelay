package authz

import "tailscale.com/tailcfg"

// Identity is the caller of a request as established by Tailscale.
type Identity struct {
	UserID   tailcfg.UserID // zero for tagged nodes
	Login    string         // empty for tagged nodes
	NodeID   tailcfg.StableNodeID
	NodeName string // first label of the node's MagicDNS name
	Tags     []string
	Grants   Effective
}

// IsUser reports whether the identity is an untagged, user-owned node.
// Only users may own hooks.
func (id Identity) IsUser() bool {
	return len(id.Tags) == 0 && !id.UserID.IsZero()
}
