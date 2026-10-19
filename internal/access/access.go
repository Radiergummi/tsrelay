// Package access is the one place every authorization rule lives. It is pure:
// callers identify the caller and load the hook, and these functions decide.
// Hidden means the caller may not learn the hook exists; Forbidden means it
// may, but not do this.
package access

import (
	"errors"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"tailscale.com/tailcfg"
)

var (
	ErrHidden    = errors.New("access: not visible")
	ErrForbidden = errors.New("access: not permitted")
)

// Why a node may not serve a hook, for audit and revocation text.
var (
	ErrNotUser    = errors.New("not a user-owned node")
	ErrOtherUser  = errors.New("node belongs to another user")
	ErrNoReceiver = errors.New("no receiver role")
	ErrChannel    = errors.New("channel not granted to receiver")
	ErrNoPublic   = errors.New("public access not granted for channel")
)

// Hook is the part of a hook the rules look at.
type Hook struct {
	Owner   tailcfg.UserID
	Channel string
}

// Level is how much of a hook the caller may see.
type Level int

const (
	None Level = iota
	Admin
	Owner
)

// View reports the caller's relation to the hook. Ownership needs no grant, so
// a user who lost the receiver role still sees their own hooks.
func View(v authz.Identity, h Hook) Level {
	switch {
	case v.IsUser() && v.UserID == h.Owner:
		return Owner
	case v.IsUser() && v.Grants.Allows(authz.RoleAdmin, h.Channel):
		return Admin
	}
	return None
}

// IsAdmin reports whether v is a user node with any admin grant.
func IsAdmin(v authz.Identity) bool { return v.IsUser() && v.Grants.Has(authz.RoleAdmin) }

// MayCreate reports whether id may create hooks. The channel and public flag
// are checked against the hook's fields by the caller.
func MayCreate(id authz.Identity) bool { return id.IsUser() && id.Grants.Has(authz.RoleReceiver) }

// MayAttach reports whether id may attach tunnels at all; Serve decides each
// hook.
func MayAttach(id authz.Identity) bool { return id.IsUser() && id.Grants.Has(authz.RoleReceiver) }

func MayReceive(g authz.Effective, channel string) bool {
	return g.Allows(authz.RoleReceiver, channel)
}

func MayPublish(g authz.Effective, channel string) bool { return g.AllowsPublic(channel) }

// Edit requires ownership and the receiver role. The hook's channel is not
// checked: the caller validates the resulting channel, so an owner can move a
// hook into a granted channel but not edit it in place.
func Edit(id authz.Identity, h Hook) error {
	switch lvl := View(id, h); {
	case lvl == None:
		return ErrHidden
	case lvl != Owner || !id.Grants.Has(authz.RoleReceiver):
		return ErrForbidden
	}
	return nil
}

// Act is Edit plus the receiver grant covering the hook's current channel
// (rotate, replay, test).
func Act(id authz.Identity, h Hook) error {
	if err := Edit(id, h); err != nil {
		return err
	}
	if !MayReceive(id.Grants, h.Channel) {
		return ErrForbidden
	}
	return nil
}

// Delete needs only visibility, so owners can always clean up and admins can
// remove hooks in their channels.
func Delete(id authz.Identity, h Hook) error {
	if View(id, h) == None {
		return ErrHidden
	}
	return nil
}

// Serve decides whether node may serve h's tunnel.
func Serve(node authz.Identity, h Hook) error {
	switch {
	case !node.IsUser():
		return ErrNotUser
	case node.UserID != h.Owner:
		return ErrOtherUser
	case !node.Grants.Has(authz.RoleReceiver):
		return ErrNoReceiver
	case !MayReceive(node.Grants, h.Channel):
		return ErrChannel
	}
	return nil
}

// ServeInternet decides whether node may serve h on the internet.
func ServeInternet(node authz.Identity, h Hook) error {
	if err := Serve(node, h); err != nil {
		return err
	}
	if !MayPublish(node.Grants, h.Channel) {
		return ErrNoPublic
	}
	return nil
}

// Send reports whether sender's role allows h's channel.
func Send(sender authz.Identity, h Hook) bool {
	return sender.Grants.Allows(authz.RoleSender, h.Channel)
}

// MaySend reports whether sender may call hooks at all; Send decides each
// hook.
func MaySend(sender authz.Identity) bool { return sender.Grants.Has(authz.RoleSender) }

// MayStream reports whether id holds any role that can watch events; what it
// sees is still filtered by View.
func MayStream(id authz.Identity) bool {
	g := id.Grants
	return g.Has(authz.RoleReceiver) || g.Has(authz.RoleSender) || g.Has(authz.RoleAdmin)
}
