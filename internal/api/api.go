// Package api serves the relay's JSON API under /api/v1 (DESIGN.md §10.1).
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

type API struct {
	Identifier identify.Identifier
	Hooks      *hooks.Manager
	Tunnels    *tunnel.Registry
	Audit      *audit.Logger
	BaseURL    string // e.g. "https://tsrelay.example.ts.net", no trailing slash
	Tunnel     tunnel.Options
}

type Me struct {
	Login  string                  `json:"login,omitempty"`
	Node   string                  `json:"node"`
	Tags   []string                `json:"tags,omitempty"`
	Slug   string                  `json:"slug,omitempty"`
	Roles  map[authz.Role][]string `json:"roles"`
	Limits Limits                  `json:"limits"`
	Hooks  int                     `json:"hooks"`
}

type Limits struct {
	MaxHooks   int    `json:"maxHooks"`
	MaxTunnels int    `json:"maxTunnels"`
	MaxHookTTL string `json:"maxHookTTL,omitempty"` // empty means unlimited
}

type Hook struct {
	ID          string          `json:"id"`
	URL         string          `json:"url"`
	Owner       string          `json:"owner"`
	Path        string          `json:"path"`
	Channel     string          `json:"channel"`
	Description string          `json:"description,omitempty"`
	Ephemeral   bool            `json:"ephemeral"`
	ExpiresAt   *time.Time      `json:"expiresAt,omitempty"`
	Settings    hooks.Settings  `json:"settings"`
	Effective   hooks.Effective `json:"effective"`
	CreatedAt   time.Time       `json:"createdAt"`
	Tunnel      *TunnelInfo     `json:"tunnel,omitempty"`
}

type TunnelInfo struct {
	ID   string `json:"id"`
	Node string `json:"node"`
}

func (a *API) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/me", a.authed(a.me))
	mux.Handle("GET /api/v1/hooks", a.authed(a.listHooks))
	mux.Handle("POST /api/v1/hooks", a.authed(a.createHook))
	mux.Handle("GET /api/v1/hooks/{id}", a.authed(a.getHook))
	mux.Handle("PATCH /api/v1/hooks/{id}", a.authed(a.updateHook))
	mux.Handle("DELETE /api/v1/hooks/{id}", a.authed(a.deleteHook))
	mux.Handle("GET /api/v1/tunnel", a.authed(a.tunnel))
}

func (a *API) authed(f func(http.ResponseWriter, *http.Request, authz.Identity)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := a.Identifier.Identify(r.Context(), r.RemoteAddr)
		if err != nil {
			problem.New(http.StatusForbidden, problem.Forbidden, "could not identify you on the tailnet").Write(w)
			return
		}
		f(w, r, id)
	})
}

func (a *API) me(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	me := Me{
		Login: id.Login, Node: id.NodeName, Tags: id.Tags, Roles: id.Grants.Patterns,
		Limits: Limits{MaxHooks: id.Grants.Limits.MaxHooks, MaxTunnels: id.Grants.Limits.MaxTunnels},
	}
	if ttl := id.Grants.Limits.MaxHookTTL; ttl > 0 {
		me.Limits.MaxHookTTL = ttl.String()
	}
	if id.IsUser() {
		me.Slug, _ = a.Hooks.Slug(id.Login)
		me.Hooks = len(a.Hooks.List(id.UserID))
	}
	writeJSON(w, http.StatusOK, me)
}

func (a *API) listHooks(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	var list []hooks.Hook
	switch {
	case r.URL.Query().Get("all") == "true" && id.Grants.Has(authz.RoleAdmin):
		list = a.Hooks.ListAll()
	case id.IsUser():
		list = a.Hooks.List(id.UserID)
	}
	out := make([]Hook, len(list))
	for i, h := range list {
		out[i] = a.hookJSON(h)
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) createHook(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	var req hooks.CreateRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		problem.New(http.StatusBadRequest, problem.BadRequest, "invalid JSON: "+err.Error()).Write(w)
		return
	}
	h, err := a.Hooks.Create(r.Context(), id, req)
	var ve hooks.ValidationError
	switch {
	case errors.Is(err, hooks.ErrForbidden):
		a.Audit.Log(r.Context(), audit.AuthzDeny, audit.Actor(id), slog.String("action", "hook.create"), slog.String("reason", "no receiver role"))
		problem.New(http.StatusForbidden, problem.Forbidden, "creating hooks requires the receiver role, and only users (not tagged nodes) can own hooks").Write(w)
		return
	case errors.As(err, &ve):
		p := problem.New(http.StatusUnprocessableEntity, problem.Invalid, ve.Error())
		p.Errors = ve
		p.Write(w)
		return
	case err != nil:
		a.Audit.Slog().Error("hook.create failed", "error", err)
		problem.New(http.StatusInternalServerError, problem.Internal, "could not save the hook").Write(w)
		return
	}
	a.Audit.Log(r.Context(), audit.HookCreate, audit.Actor(id), audit.Hook(h), slog.Bool("ephemeral", h.Ephemeral))
	w.Header().Set("Location", "/api/v1/hooks/"+string(h.ID))
	writeJSON(w, http.StatusCreated, a.hookJSON(h))
}

func (a *API) getHook(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	h, ok := a.visibleHook(r, id)
	if !ok {
		problem.New(http.StatusNotFound, problem.NotFound, "hook not found").Write(w)
		return
	}
	writeJSON(w, http.StatusOK, a.hookJSON(h))
}

func (a *API) updateHook(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	var req hooks.UpdateRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		problem.New(http.StatusBadRequest, problem.BadRequest, "invalid JSON: "+err.Error()).Write(w)
		return
	}
	h, changed, err := a.Hooks.Update(r.Context(), id, hooks.ID(r.PathValue("id")), req)
	var ve hooks.ValidationError
	switch {
	case errors.Is(err, hooks.ErrNotFound):
		problem.New(http.StatusNotFound, problem.NotFound, "hook not found").Write(w)
		return
	case errors.As(err, &ve):
		p := problem.New(http.StatusUnprocessableEntity, problem.Invalid, ve.Error())
		p.Errors = ve
		p.Write(w)
		return
	case err != nil:
		a.Audit.Slog().Error("hook.update failed", "error", err)
		problem.New(http.StatusInternalServerError, problem.Internal, "could not save the hook").Write(w)
		return
	}
	a.Audit.Log(r.Context(), audit.HookUpdate, audit.Actor(id), audit.Hook(h), slog.Any("changed", changed))
	if t, ok := a.Tunnels.Lookup(string(h.ID)); ok {
		t.Notify(tunnel.Control{Type: tunnel.ControlHookUpdated, Hooks: []tunnel.HookInfo{{ID: string(h.ID), URL: a.BaseURL + h.Route()}}}, false)
	}
	writeJSON(w, http.StatusOK, a.hookJSON(h))
}

func (a *API) deleteHook(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	h, ok := a.visibleHook(r, id)
	if !ok {
		problem.New(http.StatusNotFound, problem.NotFound, "hook not found").Write(w)
		return
	}
	if err := a.Hooks.Delete(r.Context(), h.ID); err != nil && !errors.Is(err, hooks.ErrNotFound) {
		a.Audit.Slog().Error("hook.delete failed", "error", err)
		problem.New(http.StatusInternalServerError, problem.Internal, "could not delete the hook").Write(w)
		return
	}
	a.Tunnels.Release(string(h.ID)) // calls held for the hook fail now
	if t, orphaned, attached := a.Tunnels.DetachHook(string(h.ID)); attached {
		t.Notify(tunnel.Control{Type: tunnel.ControlHookDeleted, Hooks: []tunnel.HookInfo{{ID: string(h.ID)}}}, orphaned)
	}
	a.Audit.Log(r.Context(), audit.HookDelete, audit.Actor(id), audit.Hook(h))
	w.WriteHeader(http.StatusNoContent)
}

// visibleHook returns the hook named in the path if id owns it or is an admin.
func (a *API) visibleHook(r *http.Request, id authz.Identity) (hooks.Hook, bool) {
	h, ok := a.Hooks.Get(hooks.ID(r.PathValue("id")))
	if !ok {
		return hooks.Hook{}, false
	}
	return h, (id.IsUser() && h.Owner == id.UserID) || id.Grants.Has(authz.RoleAdmin)
}

func (a *API) hookJSON(h hooks.Hook) Hook {
	out := Hook{
		ID: string(h.ID), URL: a.BaseURL + h.Route(), Owner: h.OwnerLogin, Path: h.Path,
		Channel: h.Channel, Description: h.Description, Ephemeral: h.Ephemeral, ExpiresAt: h.ExpiresAt,
		Settings: h.Settings, Effective: a.Hooks.Effective(h), CreatedAt: h.CreatedAt,
	}
	if t, ok := a.Tunnels.Lookup(string(h.ID)); ok {
		out.Tunnel = &TunnelInfo{ID: t.ID, Node: t.Owner.NodeName}
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
