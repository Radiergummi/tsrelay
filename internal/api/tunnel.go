package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/events"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/ids"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

func (a *API) tunnel(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	ctx, span := a.startTunnelSpan(r)
	r = r.WithContext(ctx)
	attached := false
	var validatedHookIDs []string // set once the requested hooks pass their ownership checks
	defer func() { a.endAttach(span, id, attached, validatedHookIDs) }()
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
	// Checks and registration must not interleave with this user's other
	// attaches, or two of them could both pass maxTunnels or "reject".
	defer a.lockAttaches(id.UserID)()
	session := r.URL.Query().Get("session")
	resume := r.URL.Query().Get("resume") == "1"
	requested := make([]hooks.Hook, 0, len(hookIDs))
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
		if cur, ok := a.Tunnels.Lookup(hid); ok {
			if resume && cur.Session != session {
				problem.New(http.StatusConflict, problem.Conflict,
					fmt.Sprintf("hook %s is now served by another tunnel; start tsrelay again to take it over", hid)).Write(w)
				return
			}
			// Under "reject", only the serving session may replace its own
			// (stale) tunnel.
			if a.Hooks.Effective(h).AttachPolicy == hooks.AttachReject && (session == "" || cur.Session != session) {
				problem.New(http.StatusConflict, problem.Conflict,
					fmt.Sprintf("hook %s is served by another tunnel, and its attach policy is %q; stop that tunnel first", hid, hooks.AttachReject)).Write(w)
				return
			}
		}
		requested = append(requested, h)
	}
	validatedHookIDs = hookIDs // every one passed the checks above, or the attach was refused
	if n, limit := a.Tunnels.CountOwner(id.UserID, hookIDs...), id.Grants.Limits.MaxTunnels; n >= limit {
		a.Audit.Log(r.Context(), audit.AuthzDeny, audit.Actor(id), slog.String("action", "tunnel.attach"), slog.String("reason", "maxTunnels"))
		problem.New(http.StatusForbidden, problem.Forbidden,
			fmt.Sprintf("you already have %d tunnels attached, the most your grants allow (maxTunnels %d); stop one first", n, limit)).Write(w)
		return
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
	if span != nil {
		span.SetAttributes(attribute.String("tsrelay.tunnel.id", t.ID))
	}
	t.Session = session
	t.RemoteAddr = r.RemoteAddr
	t.Target = localTarget(r.URL.Query().Get("target"))
	attached = true
	// r.Context() ends when this handler returns; the tunnel outlives it.
	a.register(context.WithoutCancel(ctx), t, id, requested)
}

// localTarget returns s if it is an http(s) URL without credentials, else
// "", with any query string and fragment stripped. The target is shown
// with the hook's tunnel and never connected to.
func localTarget(s string) string {
	if len(s) > 2048 {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	u.RawQuery, u.ForceQuery = "", false
	u.Fragment, u.RawFragment = "", ""
	return u.String()
}

// register attaches t to the requested hooks, tells the client, and
// detaches t again once it ends.
func (a *API) register(ctx context.Context, t *tunnel.Tunnel, id authz.Identity, requested []hooks.Hook) {
	attachedAt := time.Now()
	hookIDs := make([]string, len(requested))
	for i, h := range requested {
		hookIDs[i] = string(h.ID)
	}
	for _, rep := range a.Tunnels.Attach(t, hookIDs) {
		a.Audit.Log(ctx, audit.TunnelReplaced, slog.String("tunnel", rep.Tunnel.ID), slog.String("by", t.ID), slog.Any("hooks", rep.Hooks))
		infos := make([]tunnel.HookInfo, len(rep.Hooks))
		for i, h := range rep.Hooks {
			infos[i] = tunnel.HookInfo{ID: h}
		}
		rep.Tunnel.Notify(tunnel.Control{Type: tunnel.ControlReplaced, Hooks: infos}, rep.Orphaned)
	}

	// A hook deleted (or expired) after the checks above but before Attach
	// found no tunnel to detach, so it would stay in the registry for good.
	// Look again now that t is registered.
	var live []string
	var infos, gone []tunnel.HookInfo
	orphaned := false
	for _, h := range requested {
		if _, ok := a.Hooks.Get(h.ID); ok {
			a.Hooks.SetAttached(h.ID, true)
			live = append(live, string(h.ID))
			infos = append(infos, tunnel.HookInfo{ID: string(h.ID), URL: a.BaseURL + h.Route()})
			continue
		}
		dt, o, ok := a.Tunnels.DetachHook(string(h.ID))
		switch {
		case !ok:
		case dt == t:
			gone, orphaned = append(gone, tunnel.HookInfo{ID: string(h.ID)}), o
		default: // another tunnel took the hook over meanwhile
			dt.Notify(tunnel.Control{Type: tunnel.ControlHookDeleted, Hooks: []tunnel.HookInfo{{ID: string(h.ID)}}}, o)
		}
	}
	for _, hid := range live {
		a.Events.Publish(events.HookChanged(id.UserID, hid))
	}
	a.Audit.Log(ctx, audit.TunnelAttach, audit.Actor(id), slog.String("tunnel", t.ID), slog.Any("hooks", live))
	if len(infos) > 0 {
		t.Notify(tunnel.Control{Type: tunnel.ControlAttached, Hooks: infos}, false)
	}
	if len(gone) > 0 {
		t.Notify(tunnel.Control{Type: tunnel.ControlHookDeleted, Hooks: gone}, orphaned)
	}

	go func() {
		<-t.Done()
		for _, hid := range a.Tunnels.Detach(t) {
			a.Hooks.SetAttached(hooks.ID(hid), false)
			a.Events.Publish(events.HookChanged(id.UserID, hid))
		}
		a.Audit.Log(ctx, audit.TunnelDetach, audit.Actor(id), slog.String("tunnel", t.ID))
		a.traceDetach(ctx, t, time.Since(attachedAt))
	}()
}
