// Package api serves the relay's resources (DESIGN.md §10.1). Each URL
// answers in JSON, as the web UI (HTML), or as an event stream, whichever
// the client asks for (package negotiate).
package api

import (
	"cmp"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/Radiergummi/tsrelay/internal/access"
	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/events"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/lifecycle"
	"github.com/Radiergummi/tsrelay/internal/negotiate"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/relay"
	"github.com/Radiergummi/tsrelay/internal/release"
	"github.com/Radiergummi/tsrelay/internal/sbom"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"golang.org/x/mod/semver"
	"tailscale.com/tailcfg"
)

type API struct {
	Identifier    identify.Identifier
	Hooks         *hooks.Manager
	Tunnels       *lifecycle.Lifecycle
	Inspect       *inspect.Inspector
	Relay         *relay.Handler // delivers replays and test requests
	Events        *events.Hub    // receives hook changes; the event stream reads it
	Audit         *audit.Logger
	BaseURL       string           // e.g. "https://tsrelay.example.ts.net", no trailing slash
	PublicBaseURL string           // the relay's URL on the internet, through Funnel; empty: Funnel is off
	CapName       string           // the capability carrying relay grants; GET / names it for the no-access page
	Version       string           // the relay's version ("v0.3.0", or "dev"); GET / names it next to the CLI's download
	Releases      *release.Checker // the latest release, which GET / names for admins when newer; nil: none
	Commit        string           // the revision the relay was built from; empty when unknown
	Tunnel        tunnel.Options
	HTML          http.Handler // the web UI, for resources asked for as HTML; nil: JSON and streams only
	Tracer        trace.Tracer // nil: no spans
	Meter         metric.Meter // nil: no API latency histogram
	// StreamsPerNode caps the event streams one node may hold open; 0 means 8.
	StreamsPerNode int
	// ReidentifyEvery is how often an open event stream identifies its
	// viewer again; 0 means never.
	ReidentifyEvery time.Duration

	streamsMu sync.Mutex
	streams   map[tailcfg.StableNodeID]int // open event streams per node
}

// VersionHeader names the relay's version ("v0.3.0", or "dev") on every
// response but a callback's. The CLI compares it with its own.
const VersionHeader = "Tsrelay-Version"

// Me is the entry document, GET /: who the caller is, what the relay
// allows them, and where its resources are.
type Me struct {
	Login   string                  `json:"login,omitempty"`
	Node    string                  `json:"node"`
	Tags    []string                `json:"tags,omitempty"`
	Slug    string                  `json:"slug,omitempty"`
	Roles   map[authz.Role][]string `json:"roles"`
	Limits  Limits                  `json:"limits"`
	Hooks   int                     `json:"hooks"`
	CapName string                  `json:"capName"`
	Version string                  `json:"version"`
	Commit  string                  `json:"commit,omitempty"`
	Policy  Policy                  `json:"policy"`
	Public  []string                `json:"public"` // channel patterns in which the caller may make hooks public
	Funnel  bool                    `json:"funnel"` // the relay serves public hooks
	Links   map[string]string       `json:"links"`
	// For admins, a release newer than Version, and its release notes.
	LatestVersion string `json:"latestVersion,omitempty"`
	ReleaseURL    string `json:"releaseURL,omitempty"`
}

// links point from the entry document to the relay's resources. A build
// without an SBOM has no licenses to link to.
var links = func() map[string]string {
	l := map[string]string{"hooks": "/hooks", "channels": "/channels", "tunnel": "/tunnel", "openapi": "/openapi.json", "licenses": "/licenses"}
	if !sbom.Available() {
		delete(l, "licenses")
	}
	return l
}()

// Policy is the relay-wide setting defaults and maxima (DESIGN.md §8), which
// the UI shows next to each setting, and how many calls the relay keeps per
// hook (DESIGN.md §9.2), so the UI's cached list never grows past it.
type Policy struct {
	Defaults     hooks.Effective `json:"defaults"`
	Max          hooks.Maxima    `json:"max"`
	CallsPerHook int             `json:"callsPerHook"`
	NoBodies     bool            `json:"noBodies,omitempty"` // the relay keeps no bodies, whatever hooks set
}

type Limits struct {
	MaxHooks   int    `json:"maxHooks"`
	MaxTunnels int    `json:"maxTunnels"`
	MaxHookTTL string `json:"maxHookTTL,omitempty"` // empty means unlimited
}

type Hook struct {
	ID          string           `json:"id"`
	URL         string           `json:"url"`
	Owner       string           `json:"owner"`
	Path        string           `json:"path"`
	Channel     string           `json:"channel"`
	Description string           `json:"description,omitempty"`
	Ephemeral   bool             `json:"ephemeral"`
	Public      bool             `json:"public"`
	PublicURL   string           `json:"publicUrl,omitempty"` // owner only, and only while the relay serves Funnel
	ExpiresAt   *time.Time       `json:"expiresAt,omitempty"`
	Settings    hooks.Settings   `json:"settings"`
	Effective   hooks.Effective  `json:"effective"`
	CreatedAt   time.Time        `json:"createdAt"`
	Tunnel      *TunnelInfo      `json:"tunnel,omitempty"`
	LastCall    *inspect.Summary `json:"lastCall,omitempty"` // the newest call, metadata only
}

type TunnelInfo struct {
	ID     string `json:"id"`
	Node   string `json:"node"`
	Target string `json:"target,omitempty"`
}

// negotiated serves a resource as JSON from h, as events from stream (if
// it has one), or as the web UI.
func (a *API) negotiated(h http.Handler, stream func(http.ResponseWriter, *http.Request, authz.Identity)) http.Handler {
	offers := []negotiate.Type{negotiate.JSON}
	if a.HTML != nil {
		offers = append(offers, negotiate.HTML)
	}
	var events http.Handler
	if stream != nil {
		offers = append(offers, negotiate.EventStream)
		events = a.authed(stream)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch t, ok := negotiate.Pick(w, r, offers...); {
		case !ok:
			notAcceptable(w, offers)
		case t == negotiate.HTML:
			a.HTML.ServeHTTP(w, r)
		case t == negotiate.EventStream:
			events.ServeHTTP(w, r)
		default:
			h.ServeHTTP(w, r)
		}
	})
}

// entry serves the entry document as JSON from h, and sends browsers on to
// the hook list.
func (a *API) entry(h http.Handler) http.Handler {
	offers := []negotiate.Type{negotiate.JSON, negotiate.HTML}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch t, ok := negotiate.Pick(w, r, offers...); {
		case !ok:
			notAcceptable(w, offers)
		case t == negotiate.HTML:
			http.Redirect(w, r, "/hooks", http.StatusFound)
		default:
			h.ServeHTTP(w, r)
		}
	})
}

func notAcceptable(w http.ResponseWriter, offers []negotiate.Type) {
	if len(offers) == 0 {
		problem.New(http.StatusNotAcceptable, problem.NotAcceptable, "this document has no other representation").Write(w)
		return
	}
	types := make([]string, len(offers))
	for i, t := range offers {
		types[i] = t.String()
	}
	problem.New(http.StatusNotAcceptable, problem.NotAcceptable, "this resource is available as "+strings.Join(types, ", ")).Write(w)
}

// NotIdentified is the detail of the forbidden problem for a caller the
// tailnet could not identify; the CLI explains it by this text.
const NotIdentified = "could not identify you on the tailnet"

func (a *API) authed(f func(http.ResponseWriter, *http.Request, authz.Identity)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := a.Identifier.Identify(r.Context(), r.RemoteAddr)
		if err != nil {
			problem.New(http.StatusForbidden, problem.Forbidden, NotIdentified).Write(w)
			return
		}
		if span := trace.SpanFromContext(r.Context()); span.IsRecording() {
			span.SetAttributes(attribute.String("tsrelay.actor.login", cmp.Or(id.Login, id.NodeName)))
		}
		f(w, r, id)
	})
}

// MeOf describes id as GET / does.
func (a *API) MeOf(id authz.Identity) Me {
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
	p := a.Hooks.Policy()
	me.CapName, me.Version, me.Commit, me.Policy = a.CapName, a.Version, a.Commit, Policy{Defaults: p.Defaults, Max: p.Max, CallsPerHook: a.Inspect.CallsPerHook(), NoBodies: a.Inspect.NoBodies()}
	me.Public = append([]string{}, id.Grants.PublicPatterns...)
	me.Funnel = a.PublicBaseURL != ""
	me.Links = links
	if a.Releases != nil && access.IsAdmin(id) {
		if r := a.Releases.Latest(); semver.Compare(r.Version, a.Version) > 0 {
			me.LatestVersion, me.ReleaseURL = r.Version, r.URL
		}
	}
	return me
}

func (a *API) me(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	writeJSON(w, http.StatusOK, a.MeOf(id))
}

func (a *API) listHooks(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	list, _ := a.scopedHooks(r, id)
	channel := r.URL.Query().Get("channel")
	out := make([]Hook, 0, len(list))
	for _, h := range list {
		if channel == "" || h.Channel == channel {
			out = append(out, a.HookJSON(h, id))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) createHook(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	var req hooks.CreateRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	dry := queryBool(r, "dryRun")
	create := a.Tunnels.Create
	if dry {
		create = a.Hooks.CheckCreate
	}
	h, err := create(r.Context(), id, req)
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
	if dry {
		writeJSON(w, http.StatusOK, a.HookJSON(h, id))
		return
	}
	w.Header().Set("Location", "/hooks/"+string(h.ID))
	writeJSON(w, http.StatusCreated, a.HookJSON(h, id))
}

func (a *API) getHook(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	h, _, ok := a.visibleHook(r, id)
	if !ok {
		problem.New(http.StatusNotFound, problem.NotFound, "hook not found").Write(w)
		return
	}
	writeJSON(w, http.StatusOK, a.HookJSON(h, id))
}

func (a *API) updateHook(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	var req hooks.UpdateRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	// The store answers not found to anyone but the owner, so decide 404
	// against 403 here first.
	if _, err := a.decide(r, id, access.Edit); err != nil {
		writeAccessError(w, err, "only the hook's owner, with the receiver role, can edit it")
		return
	}
	dry := queryBool(r, "dryRun")
	update := a.Tunnels.Update
	if dry {
		update = a.Hooks.CheckUpdate
	}
	h, _, err := update(r.Context(), id, hooks.ID(r.PathValue("id")), req)
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
	if dry {
		writeJSON(w, http.StatusOK, a.HookJSON(h, id))
		return
	}
	writeJSON(w, http.StatusOK, a.HookJSON(h, id))
}

func (a *API) rotatePublicURL(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	cur, err := a.decide(r, id, access.Act)
	if err != nil {
		writeAccessError(w, err, "only the hook's owner, with the receiver role for its channel, can rotate its public URL")
		return
	}
	h, err := a.Tunnels.RotatePublicToken(r.Context(), id, cur.ID)
	switch {
	case errors.Is(err, hooks.ErrNotFound):
		problem.New(http.StatusNotFound, problem.NotFound, "hook not found").Write(w)
		return
	case errors.Is(err, hooks.ErrNotPublic):
		problem.New(http.StatusConflict, problem.Conflict, "the hook is not public; make it public first").Write(w)
		return
	case err != nil:
		a.Audit.Slog().Error("hook.update failed", "error", err)
		problem.New(http.StatusInternalServerError, problem.Internal, "could not save the hook").Write(w)
		return
	}
	writeJSON(w, http.StatusOK, a.HookJSON(h, id))
}

func (a *API) deleteHook(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	h, err := a.decide(r, id, access.Delete)
	if err != nil {
		writeAccessError(w, err, "")
		return
	}
	err = a.Tunnels.Delete(r.Context(), id, h.ID)
	if errors.Is(err, hooks.ErrNotFound) {
		writeAccessError(w, access.ErrHidden, "") // it moved out of id's sight since the check
		return
	}
	if err != nil {
		a.Audit.Slog().Error("hook.delete failed", "error", err)
		problem.New(http.StatusInternalServerError, problem.Internal, "could not delete the hook").Write(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// visibleHook returns the hook named in the path if id may see it.
func (a *API) visibleHook(r *http.Request, id authz.Identity) (hooks.Hook, access.Level, bool) {
	return a.VisibleHook(hooks.ID(r.PathValue("id")), id)
}

// VisibleHook returns the hook with the given ID, and who's view of it, if
// who may see it.
func (a *API) VisibleHook(id hooks.ID, who authz.Identity) (hooks.Hook, access.Level, bool) {
	h, ok := a.Hooks.Get(id)
	if !ok {
		return hooks.Hook{}, access.None, false
	}
	lvl := access.View(who, h.Access())
	if lvl == access.None {
		return hooks.Hook{}, access.None, false
	}
	return h, lvl, true
}

// decide applies rule to the hook named in the path. A hook that does not
// exist is hidden, whoever asks.
func (a *API) decide(r *http.Request, id authz.Identity, rule func(authz.Identity, access.Hook) error) (hooks.Hook, error) {
	h, ok := a.Hooks.Get(hooks.ID(r.PathValue("id")))
	if !ok {
		return hooks.Hook{}, access.ErrHidden
	}
	return h, rule(id, h.Access())
}

// writeAccessError answers an access decision: a hook the caller may not see
// is not found, one they may see but not act on is forbidden, for what.
func writeAccessError(w http.ResponseWriter, err error, what string) {
	if errors.Is(err, access.ErrForbidden) {
		problem.New(http.StatusForbidden, problem.Forbidden, what).Write(w)
		return
	}
	problem.New(http.StatusNotFound, problem.NotFound, "hook not found").Write(w)
}

// HookJSON is h as the API shows it to viewer: URL, effective settings,
// attached tunnel, and (for the owner only) the public URL.
func (a *API) HookJSON(h hooks.Hook, viewer authz.Identity) Hook {
	out := Hook{
		ID: string(h.ID), URL: a.BaseURL + h.Route(), Owner: h.OwnerLogin, Path: h.Path,
		Channel: h.Channel, Description: h.Description, Ephemeral: h.Ephemeral, Public: h.Public, ExpiresAt: h.ExpiresAt,
		Settings: h.Settings, Effective: a.Hooks.Effective(h), CreatedAt: h.CreatedAt,
	}
	if access.View(viewer, h.Access()) == access.Owner { // the URL works like a password
		out.PublicURL = h.PublicURL(a.PublicBaseURL)
	}
	if t, ok := a.Tunnels.Lookup(h.ID); ok {
		out.Tunnel = &TunnelInfo{ID: t.ID, Node: t.Owner.NodeName, Target: t.Target}
	}
	if last, ok := a.Inspect.Latest(h.ID); ok {
		out.LastCall = &last
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
