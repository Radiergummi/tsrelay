// Package audit writes the relay's structured audit log (DESIGN.md §13).
// Records never contain bodies, query strings, or header values.
package audit

import (
	"context"
	"log/slog"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
)

const (
	TunnelAttach   = "tunnel.attach"
	TunnelDetach   = "tunnel.detach"
	TunnelReplaced = "tunnel.replaced"
	TunnelRevoked  = "tunnel.revoked"
	HookCreate     = "hook.create"
	HookUpdate     = "hook.update"
	HookDelete     = "hook.delete"
	HookExpire     = "hook.expire"
	Call           = "call"
	AuthzDeny      = "authz.deny"
)

type Logger struct{ l *slog.Logger }

func New(l *slog.Logger) *Logger { return &Logger{l: l} }

// Slog returns the underlying logger, e.g. for identify.Tailscale.
func (a *Logger) Slog() *slog.Logger { return a.l }

// Log writes one audit event; the event name is the record's message.
func (a *Logger) Log(ctx context.Context, event string, attrs ...slog.Attr) {
	a.l.LogAttrs(ctx, slog.LevelInfo, event, attrs...)
}

// Actor describes the node that caused an event.
func Actor(id authz.Identity) slog.Attr {
	return slog.Group("actor", "node", id.NodeName, "login", id.Login, "tags", id.Tags)
}

func Hook(h hooks.Hook) slog.Attr {
	return slog.Group("hook", "id", string(h.ID), "owner", h.OwnerLogin, "path", h.Path, "channel", h.Channel)
}
