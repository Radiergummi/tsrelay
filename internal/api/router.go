package api

import (
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/install"
	"github.com/Radiergummi/tsrelay/internal/negotiate"
	"github.com/Radiergummi/tsrelay/internal/problem"
)

// RouterConfig is what NewRouter serves besides the API itself.
type RouterConfig struct {
	API           *API
	Inspect       http.Handler // the redirect for the CLI's inspect links
	Fallback      http.Handler // every request no route claims: the web UI and its assets
	ReleaseAssets string       // base URL of the release assets /download redirects to; "" means GitHub's
	AllowedHosts  []string     // Host header values the tailnet handler answers to
}

// Router is the relay's whole URL space (DESIGN.md §10.1).
type Router struct {
	Tailnet http.Handler // callbacks, the API, the web UI and the install scripts
	Public  http.Handler // public hooks, for the Funnel listener; nil unless API.PublicBaseURL is set
	Routes  []string     // every pattern registered, in order
}

// routeRecorder is the mux NewRouter registers on: an http.ServeMux that
// also remembers its patterns, which the stdlib mux cannot list.
type routeRecorder struct {
	mux      *http.ServeMux
	patterns []string
	writes   []string // the methods other than GET and HEAD the patterns name
}

func (m *routeRecorder) Handle(pattern string, h http.Handler) {
	m.patterns = append(m.patterns, pattern)
	if method, _, ok := strings.Cut(pattern, " "); ok && method != http.MethodGet && method != http.MethodHead && !slices.Contains(m.writes, method) {
		m.writes = append(m.writes, method)
	}
	m.mux.Handle(pattern, h)
}

// allows lists the methods r's path allows when a route of its own answers
// there, rather than the catch-all behind every path, and nil otherwise.
// GET and HEAD are always among them: the web UI answers them everywhere.
func (m *routeRecorder) allows(r *http.Request) []string {
	methods := []string{http.MethodGet, http.MethodHead}
	routed := false
	probe := *r
	for _, method := range append([]string{http.MethodGet}, m.writes...) {
		probe.Method = method
		if _, pattern := m.mux.Handler(&probe); pattern != "" && pattern != "/" {
			routed = true
			if method != http.MethodGet {
				methods = append(methods, method)
			}
		}
	}
	if !routed {
		return nil
	}
	return methods
}

// NewRouter registers every route the relay serves and puts the host guard,
// the callback split, the version header and extension handling in front.
func NewRouter(cfg RouterConfig) *Router {
	a := cfg.API
	m := &routeRecorder{mux: http.NewServeMux()}
	latency := a.latencyHistogram()
	observed := func(pattern string, f func(http.ResponseWriter, *http.Request, authz.Identity)) http.Handler {
		return a.observed(latency, pattern, a.authed(f))
	}
	route := func(pattern string, f func(http.ResponseWriter, *http.Request, authz.Identity)) {
		m.Handle(pattern, observed(pattern, f))
	}
	// A resource also answers as the web UI and, with stream, as events.
	resource := func(pattern string, f, stream func(http.ResponseWriter, *http.Request, authz.Identity)) {
		m.Handle(pattern, a.negotiated(observed(pattern, f), stream))
	}
	// A document has the representations it offers and no other (only); a
	// file name that ends like one, such as /sbom.cdx.json, keeps its
	// extension.
	var literal []string
	document := func(pattern string, h http.Handler) {
		if path, ok := strings.CutPrefix(pattern, "GET "); ok && negotiate.Extension(path) {
			literal = append(literal, path)
		}
		m.Handle(pattern, h)
	}
	// A file is a document for identified callers, who are identified
	// before an extension is refused, as on its bare path.
	file := func(pattern string, f func(http.ResponseWriter, *http.Request, authz.Identity), offers ...negotiate.Type) {
		document(pattern, observed(pattern, func(w http.ResponseWriter, r *http.Request, id authz.Identity) {
			if !refuses(w, r, offers) {
				f(w, r, id)
			}
		}))
	}
	in := &install.Handler{Version: a.Version, BaseURL: a.BaseURL, Releases: cfg.ReleaseAssets}
	m.Handle("GET /{$}", a.entry(observed("GET /", a.me)))
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
	m.Handle("GET /tunnel", a.authed(a.tunnel))
	document("GET /openapi", only(http.HandlerFunc(serveOpenAPI), negotiate.JSON))
	document("GET /openapi.yaml", only(http.HandlerFunc(serveOpenAPIYAML)))
	resource("GET /licenses", a.licenses, nil)
	file("GET /licenses/{textId}", a.licenseText)
	file("GET /notices.txt", a.notices)
	file("GET /sbom.cdx.json", a.sbomDocument, negotiate.JSON)
	m.Handle("GET /inspect/{callID}", cfg.Inspect)
	document("GET /install.sh", only(in.Shell()))
	document("GET /install.ps1", only(in.PowerShell()))
	document("GET /download/{os}/{arch}", only(in.Download()))
	m.Handle("/", catchAll(m, cfg.Fallback))

	// Callbacks bypass the mux, whose path cleaning would rewrite paths
	// such as "/~m/cb//x" that must reach the local app unchanged. They
	// also go without the version header: their responses stay exactly
	// what the receiver's app returned.
	resources := negotiate.Extensions(literal, m.mux)
	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/~") {
			a.Relay.ServeHTTP(w, r)
			return
		}
		w.Header().Set(VersionHeader, a.Version)
		resources.ServeHTTP(w, r)
	})
	rt := &Router{Tailnet: Guard(cfg.AllowedHosts, root), Routes: m.patterns}

	if a.PublicBaseURL != "" {
		u, err := url.Parse(a.PublicBaseURL)
		if err != nil {
			panic("api: invalid PublicBaseURL: " + err.Error())
		}
		guarded := Guard([]string{u.Hostname()}, http.HandlerFunc(a.Relay.ServePublic))
		rt.Public = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Nothing but public hooks: the UI, the API and tailnet hooks
			// must not be reachable from the internet, not even as errors.
			if !strings.HasPrefix(r.URL.Path, "/+") {
				http.NotFound(w, r)
				return
			}
			guarded.ServeHTTP(w, r)
		})
	}
	return rt
}

// catchAll answers every path no route claims. A method some route serves
// at the path is a wrong one; any other GET belongs to the web UI.
func catchAll(m *routeRecorder, fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if negotiate.MidPath(r.URL.Path) {
			problem.New(http.StatusNotFound, problem.NotFound, "not found").Write(w)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if allow := m.allows(r); allow != nil {
				w.Header().Set("Allow", strings.Join(allow, ", "))
				problem.New(http.StatusMethodNotAllowed, problem.MethodNotAllowed, r.Method+" is not allowed here").Write(w)
				return
			}
		}
		fallback.ServeHTTP(w, r)
	})
}

// only serves a document in the representations it offers, and refuses an
// extension asking for another: /install.sh.json is not the script.
func only(h http.Handler, offers ...negotiate.Type) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !refuses(w, r, offers) {
			h.ServeHTTP(w, r)
		}
	})
}

// refuses answers 406 when an extension on r's path asks for a
// representation not in offers, and reports whether it did.
func refuses(w http.ResponseWriter, r *http.Request, offers []negotiate.Type) bool {
	if t, ok := negotiate.Forced(r); ok && !slices.Contains(offers, t) {
		notAcceptable(w, offers)
		return true
	}
	return false
}
