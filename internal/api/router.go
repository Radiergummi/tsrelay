package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/negotiate"
	"github.com/Radiergummi/tsrelay/internal/problem"
)

// RouterConfig is what NewRouter serves besides the API itself.
type RouterConfig struct {
	API          *API
	Inspect      http.Handler // the redirect for the CLI's inspect links
	Fallback     http.Handler // every GET no route claims: the web UI and its assets
	Install      Installer
	AllowedHosts []string // Host header values the tailnet handler answers to
}

// Installer serves the install scripts and the binaries' downloads
// (package install, which imports this one through the client).
type Installer interface {
	Shell() http.Handler
	PowerShell() http.Handler
	Download() http.Handler
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
}

func (m *routeRecorder) Handle(pattern string, h http.Handler) {
	m.patterns = append(m.patterns, pattern)
	m.mux.Handle(pattern, h)
}

func (m *routeRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) { m.mux.ServeHTTP(w, r) }

// allows lists the methods a route of its own answers at r's path, rather
// than the catch-all behind every path.
func (m *routeRecorder) allows(r *http.Request) []string {
	var methods []string
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPatch, http.MethodDelete} {
		probe := r.Clone(r.Context())
		probe.Method = method
		if _, pattern := m.mux.Handler(probe); pattern != "" && pattern != "/" {
			methods = append(methods, method)
		}
	}
	return methods
}

// NewRouter registers every route the relay serves and puts the host guard,
// the callback split, the version header and extension handling in front.
func NewRouter(cfg RouterConfig) *Router {
	a := cfg.API
	m := &routeRecorder{mux: http.NewServeMux()}
	latency := a.latencyHistogram()
	route := func(pattern string, f func(http.ResponseWriter, *http.Request, authz.Identity)) {
		m.Handle(pattern, a.observed(latency, pattern, a.authed(f)))
	}
	// A resource also answers as the web UI and, with stream, as events.
	resource := func(pattern string, f, stream func(http.ResponseWriter, *http.Request, authz.Identity)) {
		m.Handle(pattern, a.negotiated(a.observed(latency, pattern, a.authed(f)), stream))
	}
	// A document has one type, which an extension cannot change.
	document := func(pattern, contentType string, h http.Handler) {
		m.Handle(pattern, only(contentType, h))
	}
	m.Handle("GET /{$}", a.entry(a.observed(latency, "GET /", a.authed(a.me))))
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
	document("GET /openapi", "application/json", http.HandlerFunc(serveOpenAPI))
	document("GET /openapi.yaml", "application/yaml", http.HandlerFunc(serveOpenAPIYAML))
	resource("GET /licenses", a.licenses, nil)
	document("GET /licenses/{textId}", "text/plain", a.observed(latency, "GET /licenses/{textId}", a.authed(a.licenseText)))
	document("GET /notices.txt", "text/plain", a.observed(latency, "GET /notices.txt", a.authed(a.notices)))
	document("GET /sbom.cdx.json", "application/vnd.cyclonedx+json", a.observed(latency, "GET /sbom.cdx.json", a.authed(a.sbomDocument)))
	m.Handle("GET /inspect/{callID}", cfg.Inspect)
	document("GET /install.sh", "text/x-shellscript", cfg.Install.Shell())
	document("GET /install.ps1", "text/plain", cfg.Install.PowerShell())
	document("GET /download/{os}/{arch}", "application/octet-stream", cfg.Install.Download())
	m.Handle("/", catchAll(m, cfg.Fallback))

	// File names that end like a representation, such as /sbom.cdx.json,
	// keep their extension.
	var literal []string
	for _, p := range m.patterns {
		if path, ok := strings.CutPrefix(p, "GET "); ok && !strings.Contains(path, "{") && negotiate.Extension(path) {
			literal = append(literal, path)
		}
	}

	// Callbacks bypass the mux, whose path cleaning would rewrite paths
	// such as "/~m/cb//x" that must reach the local app unchanged. They
	// also go without the version header: their responses stay exactly
	// what the receiver's app returned.
	resources := negotiate.Extensions(literal, m)
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
			if allow := m.allows(r); len(allow) > 0 {
				w.Header().Set("Allow", strings.Join(allow, ", "))
				problem.New(http.StatusMethodNotAllowed, problem.MethodNotAllowed, r.Method+" is not allowed here").Write(w)
				return
			}
		}
		fallback.ServeHTTP(w, r)
	})
}

// only serves a document of one type, and refuses an extension that asks
// for another: /install.sh.json is not the script.
func only(contentType string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t, ok := negotiate.Forced(r); ok && t.String() != contentType {
			problem.New(http.StatusNotAcceptable, problem.NotAcceptable, "this resource is available as "+contentType).Write(w)
			return
		}
		h.ServeHTTP(w, r)
	})
}
