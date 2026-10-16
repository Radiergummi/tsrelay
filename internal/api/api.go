// Package api serves the relay's resources (DESIGN.md §10.1). Each URL
// answers in JSON, as the web UI (HTML), or as an event stream, whichever
// the client asks for (package negotiate).
package api

import (
	"cmp"
	"context"
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

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/events"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/negotiate"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/relay"
	"github.com/Radiergummi/tsrelay/internal/release"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"golang.org/x/mod/semver"
	"tailscale.com/tailcfg"
)

type API struct {
	Identifier    identify.Identifier
	Hooks         *hooks.Manager
	Tunnels       *tunnel.Registry
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

	// attachLocks holds one *sync.Mutex per user (tailcfg.UserID). It
	// serializes that user's attaches from the checks through registration.
	attachLocks sync.Map

	notifyMu  sync.Mutex // orders updateSeq with the URL read for it
	updateSeq uint64

	routes []string // the patterns Register mounted
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
	Public  bool                    `json:"public"` // the caller may make hooks public
	Funnel  bool                    `json:"funnel"` // the relay serves public hooks
	Links   map[string]string       `json:"links"`
	// For admins, a release newer than Version, and its release notes.
	LatestVersion string `json:"latestVersion,omitempty"`
	ReleaseURL    string `json:"releaseURL,omitempty"`
}

// links point from the entry document to the relay's resources.
var links = map[string]string{"hooks": "/hooks", "channels": "/channels", "tunnel": "/tunnel", "openapi": "/openapi.json", "licenses": "/licenses"}

// Policy is the relay-wide setting defaults and maxima (DESIGN.md §8), which
// the UI shows next to each setting, and how many calls the relay keeps per
// hook (DESIGN.md §9.2), so the UI's cached list never grows past it.
type Policy struct {
	Defaults     hooks.Effective `json:"defaults"`
	Max          hooks.Maxima    `json:"max"`
	CallsPerHook int             `json:"callsPerHook"`
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

func (a *API) Register(mux *http.ServeMux) {
	latency := a.latencyHistogram()
	handle := func(pattern string, h http.Handler) {
		a.routes = append(a.routes, pattern)
		mux.Handle(pattern, h)
	}
	route := func(pattern string, f func(http.ResponseWriter, *http.Request, authz.Identity)) {
		handle(pattern, a.observed(latency, pattern, a.authed(f)))
	}
	// A resource also answers as the web UI and, with stream, as events.
	resource := func(pattern string, f, stream func(http.ResponseWriter, *http.Request, authz.Identity)) {
		handle(pattern, a.negotiated(a.observed(latency, pattern, a.authed(f)), stream))
	}
	handle("GET /{$}", a.entry(a.observed(latency, "GET /", a.authed(a.me))))
	resource("GET /hooks", a.listHooks, a.hooksStream)
	route("POST /hooks", a.createHook)
	resource("GET /hooks/{id}", a.getHook, a.hookStream)
	route("PATCH /hooks/{id}", a.updateHook)
	route("POST /hooks/{id}/rotate-public-url", a.rotatePublicURL)
	route("DELETE /hooks/{id}", a.deleteHook)
	resource("GET /hooks/{id}/calls", a.listCalls, a.callsStream)
	resource("GET /hooks/{id}/calls/{callID}", a.getCall, nil)
	route("POST /hooks/{id}/calls/{callID}/replay", a.replayCall)
	route("POST /hooks/{id}/test", a.testCall)
	resource("GET /channels", a.listChannels, nil)
	resource("GET /channels/{name}", a.getChannel, nil)
	// Long-lived: not observed (the tunnel traces its own attach and detach).
	handle("GET /tunnel", a.authed(a.tunnel))
	handle("GET /openapi", http.HandlerFunc(serveOpenAPI))
	handle("GET /openapi.yaml", http.HandlerFunc(serveOpenAPIYAML))
	resource("GET /licenses", a.licenses, nil)
	route("GET /licenses/{textId}", a.licenseText)
	route("GET /notices.txt", a.notices)
	route("GET /sbom.cdx.json", a.sbomDocument)
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
	types := make([]string, len(offers))
	for i, t := range offers {
		types[i] = t.String()
	}
	problem.New(http.StatusNotAcceptable, problem.NotAcceptable, "this resource is available as "+strings.Join(types, ", ")).Write(w)
}

// Routes are the patterns Register mounted, for checking them against the
// OpenAPI document.
func (a *API) Routes() []string { return a.routes }

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

// lockAttaches locks user's attaches and returns the unlock function.
func (a *API) lockAttaches(user tailcfg.UserID) (unlock func()) {
	v, _ := a.attachLocks.LoadOrStore(user, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
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
	me.CapName, me.Version, me.Commit, me.Policy = a.CapName, a.Version, a.Commit, Policy{Defaults: p.Defaults, Max: p.Max, CallsPerHook: a.Inspect.CallsPerHook()}
	me.Public = id.Grants.Public
	me.Funnel = a.PublicBaseURL != ""
	me.Links = links
	if a.Releases != nil && id.Grants.Has(authz.RoleAdmin) {
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
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		problem.New(http.StatusBadRequest, problem.BadRequest, "invalid JSON: "+err.Error()).Write(w)
		return
	}
	dry := r.URL.Query().Get("dryRun") == "true"
	create := a.Hooks.Create
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
	a.Audit.Log(r.Context(), audit.HookCreate, audit.Actor(id), audit.Hook(h), slog.Bool("ephemeral", h.Ephemeral))
	a.Events.Publish(events.HookChanged(h.Owner, string(h.ID)))
	w.Header().Set("Location", "/hooks/"+string(h.ID))
	writeJSON(w, http.StatusCreated, a.HookJSON(h, id))
}

func (a *API) getHook(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	h, ok := a.visibleHook(r, id)
	if !ok {
		problem.New(http.StatusNotFound, problem.NotFound, "hook not found").Write(w)
		return
	}
	writeJSON(w, http.StatusOK, a.HookJSON(h, id))
}

func (a *API) updateHook(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	var req hooks.UpdateRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		problem.New(http.StatusBadRequest, problem.BadRequest, "invalid JSON: "+err.Error()).Write(w)
		return
	}
	dry := r.URL.Query().Get("dryRun") == "true"
	update := a.Hooks.Update
	if dry {
		update = a.Hooks.CheckUpdate
	}
	h, changed, err := update(r.Context(), id, hooks.ID(r.PathValue("id")), req)
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
	a.Audit.Log(r.Context(), audit.HookUpdate, audit.Actor(id), audit.Hook(h), slog.Any("changed", changed))
	a.Events.Publish(events.HookChanged(h.Owner, string(h.ID)))
	a.notifyUpdated(h.ID)
	if h.Public {
		a.refreshTunnelPublic(r.Context(), h.ID)
	}
	writeJSON(w, http.StatusOK, a.HookJSON(h, id))
}

// refreshTunnelPublic identifies the node of the tunnel serving a public
// hook again, so a public grant added since it attached applies now rather
// than at the next revalidation. The tunnel's own node decides, not the
// caller's. It only raises the flag; the revalidator lowers it.
func (a *API) refreshTunnelPublic(ctx context.Context, hookID hooks.ID) {
	t, ok := a.Tunnels.Lookup(string(hookID))
	if !ok || t.Public() {
		return
	}
	if id, err := a.Identifier.Identify(ctx, t.RemoteAddr); err == nil && id.IsUser() && id.UserID == t.Owner.UserID && id.Grants.Public {
		t.SetPublic(true)
	}
}

func (a *API) rotatePublicURL(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	h, err := a.Hooks.RotatePublicToken(r.Context(), id, hooks.ID(r.PathValue("id")))
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
	a.Audit.Log(r.Context(), audit.HookUpdate, audit.Actor(id), audit.Hook(h), slog.Any("changed", []string{"publicUrl"}))
	a.Events.Publish(events.HookChanged(h.Owner, string(h.ID)))
	a.notifyUpdated(h.ID)
	writeJSON(w, http.StatusOK, a.HookJSON(h, id))
}

// notifyUpdated tells the tunnel serving the hook its current URL. Messages
// are sent in the background and can overtake each other, so each carries
// a sequence number. The number and the URL are taken under one lock, so a
// higher number never carries an older URL.
func (a *API) notifyUpdated(id hooks.ID) {
	a.notifyMu.Lock()
	h, ok := a.Hooks.Get(id)
	a.updateSeq++
	seq := a.updateSeq
	a.notifyMu.Unlock()
	if !ok {
		return // deleted meanwhile; the delete sends hook-deleted
	}
	if t, ok := a.Tunnels.Lookup(string(id)); ok {
		t.Notify(tunnel.Control{Type: tunnel.ControlHookUpdated, Seq: seq,
			Hooks: []tunnel.HookInfo{{ID: string(id), URL: a.BaseURL + h.Route(), Public: h.Public, PublicURL: a.publicURL(h)}}}, false)
	}
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
	a.Tunnels.RemoveHook(string(h.ID))
	a.Inspect.Forget(h.ID)
	a.Audit.Log(r.Context(), audit.HookDelete, audit.Actor(id), audit.Hook(h))
	a.Events.Publish(events.HookChanged(h.Owner, string(h.ID)))
	w.WriteHeader(http.StatusNoContent)
}

// visibleHook returns the hook named in the path if id may see it.
func (a *API) visibleHook(r *http.Request, id authz.Identity) (hooks.Hook, bool) {
	return a.VisibleHook(hooks.ID(r.PathValue("id")), id)
}

// VisibleHook returns the hook with the given ID if who may see it (Visible).
func (a *API) VisibleHook(id hooks.ID, who authz.Identity) (hooks.Hook, bool) {
	h, ok := a.Hooks.Get(id)
	if !ok || !Visible(who, h) {
		return hooks.Hook{}, false
	}
	return h, true
}

// HookJSON is h as the API shows it to viewer: URL, effective settings,
// attached tunnel, and (for the owner only) the public URL.
func (a *API) HookJSON(h hooks.Hook, viewer authz.Identity) Hook {
	out := Hook{
		ID: string(h.ID), URL: a.BaseURL + h.Route(), Owner: h.OwnerLogin, Path: h.Path,
		Channel: h.Channel, Description: h.Description, Ephemeral: h.Ephemeral, Public: h.Public, ExpiresAt: h.ExpiresAt,
		Settings: h.Settings, Effective: a.Hooks.Effective(h), CreatedAt: h.CreatedAt,
	}
	if viewer.IsUser() && viewer.UserID == h.Owner { // the URL works like a password
		out.PublicURL = a.publicURL(h)
	}
	if t, ok := a.Tunnels.Lookup(string(h.ID)); ok {
		out.Tunnel = &TunnelInfo{ID: t.ID, Node: t.Owner.NodeName, Target: t.Target}
	}
	if last, ok := a.Inspect.Latest(h.ID); ok {
		out.LastCall = &last
	}
	return out
}

// publicURL is h's URL on the internet, or "" if h is not public or the
// relay serves no Funnel.
func (a *API) publicURL(h hooks.Hook) string {
	if !h.Public || a.PublicBaseURL == "" {
		return ""
	}
	return a.PublicBaseURL + h.PublicRoute()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
