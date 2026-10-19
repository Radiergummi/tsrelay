package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/ids"
	"github.com/Radiergummi/tsrelay/internal/lifecycle"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

func (a *API) tunnel(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	ctx, span := a.startTunnelSpan(r)
	r = r.WithContext(ctx)
	attached := false
	var admittedHookIDs []string // set once Admit accepts the requested hooks
	defer func() { a.endAttach(span, id, attached, admittedHookIDs) }()
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
	requested := make([]hooks.ID, len(hookIDs))
	for i, hid := range hookIDs {
		requested[i] = hooks.ID(hid)
	}
	tk, ae := a.Tunnels.Admit(ctx, id, requested, session, resume)
	if ae != nil {
		attachRefused(ae).Write(w)
		return
	}
	defer tk.Close()
	admittedHookIDs = hookIDs

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
	attachCtx := context.WithoutCancel(ctx)
	tk.Attach(attachCtx, t, func(d time.Duration) { a.traceDetach(attachCtx, t, d) })
}

// attachRefused is the problem the client gets for ae.
func attachRefused(ae *lifecycle.AdmitError) *problem.Problem {
	switch ae.Kind {
	case lifecycle.HookNotFound:
		return problem.New(http.StatusNotFound, problem.NotFound, ae.Error())
	case lifecycle.Superseded, lifecycle.Rejected:
		return problem.New(http.StatusConflict, problem.Conflict, ae.Error())
	default: // ChannelDenied, TooManyTunnels
		return problem.New(http.StatusForbidden, problem.Forbidden, ae.Error())
	}
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
