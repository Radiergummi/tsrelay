package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/ids"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

func (a *API) tunnel(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	if !tunnel.IsUpgrade(r) {
		problem.New(http.StatusBadRequest, problem.BadRequest, "expected Connection: Upgrade and Upgrade: "+tunnel.UpgradeToken).Write(w)
		return
	}
	if !id.IsUser() || !id.Grants.Has(authz.RoleReceiver) {
		a.Audit.Log(r.Context(), audit.AuthzDeny, audit.Actor(id), slog.String("action", "tunnel.attach"), slog.String("reason", "no receiver role"))
		problem.New(http.StatusForbidden, problem.Forbidden, "attaching tunnels requires the receiver role").Write(w)
		return
	}
	hookIDs := r.URL.Query()["hook"]
	if len(hookIDs) == 0 {
		problem.New(http.StatusBadRequest, problem.BadRequest, "name at least one hook with ?hook=<id>").Write(w)
		return
	}
	session := r.URL.Query().Get("session")
	resume := r.URL.Query().Get("resume") == "1"
	attached := make([]hooks.Hook, 0, len(hookIDs))
	for _, hid := range hookIDs {
		h, ok := a.Hooks.Get(hooks.ID(hid))
		if !ok || h.Owner != id.UserID {
			problem.New(http.StatusNotFound, problem.NotFound, fmt.Sprintf("hook %s not found", hid)).Write(w)
			return
		}
		if !id.Grants.Allows(authz.RoleReceiver, h.Channel) {
			problem.New(http.StatusForbidden, problem.Forbidden, fmt.Sprintf("your grants no longer cover channel %q", h.Channel)).Write(w)
			return
		}
		if cur, ok := a.Tunnels.Lookup(hid); ok && resume && cur.Session != session {
			problem.New(http.StatusConflict, problem.Conflict,
				fmt.Sprintf("hook %s is now served by another tunnel; start tsrelay again to take it over", hid)).Write(w)
			return
		}
		attached = append(attached, h)
	}

	conn, err := tunnel.Accept(w, r)
	if err != nil {
		a.Audit.Slog().Warn("tunnel upgrade failed", "error", err)
		return
	}
	t, err := tunnel.NewTunnel(conn, ids.New("t_"), id, a.Tunnel)
	if err != nil {
		conn.Close()
		a.Audit.Slog().Warn("tunnel setup failed", "error", err)
		return
	}
	t.Session = session
	// r.Context() ends when this handler returns; the tunnel outlives it.
	ctx := context.WithoutCancel(r.Context())
	for _, rep := range a.Tunnels.Attach(t, hookIDs) {
		a.Audit.Log(ctx, audit.TunnelReplaced, slog.String("tunnel", rep.Tunnel.ID), slog.String("by", t.ID), slog.Any("hooks", rep.Hooks))
		infos := make([]tunnel.HookInfo, len(rep.Hooks))
		for i, h := range rep.Hooks {
			infos[i] = tunnel.HookInfo{ID: h}
		}
		rep.Tunnel.Notify(tunnel.Control{Type: tunnel.ControlReplaced, Hooks: infos}, rep.Orphaned)
	}
	infos := make([]tunnel.HookInfo, len(attached))
	for i, h := range attached {
		a.Hooks.SetAttached(h.ID, true)
		infos[i] = tunnel.HookInfo{ID: string(h.ID), URL: a.BaseURL + h.Route()}
	}
	a.Audit.Log(ctx, audit.TunnelAttach, audit.Actor(id), slog.String("tunnel", t.ID), slog.Any("hooks", hookIDs))
	t.Notify(tunnel.Control{Type: tunnel.ControlAttached, Hooks: infos}, false)

	go func() {
		<-t.Done()
		for _, hid := range a.Tunnels.Detach(t) {
			a.Hooks.SetAttached(hooks.ID(hid), false)
		}
		a.Audit.Log(ctx, audit.TunnelDetach, audit.Actor(id), slog.String("tunnel", t.ID))
	}()
}
