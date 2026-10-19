// Package ui serves the relay's web UI (DESIGN.md §10.2): a single-page
// app built from web/ and embedded in the binary. Every app route asked
// for as HTML gets index.html, including the API's resources; the app
// reads and writes them as JSON, so the UI has no endpoints of its own
// except the redirect for the CLI's links.
package ui

import (
	"io/fs"
	"net/http"
	"net/url"

	"github.com/Radiergummi/tsrelay/internal/api"
)

// CSP is the policy of every UI response (DESIGN.md §5.7): nothing loads
// from anywhere but the relay, and there is no inline script or style.
const CSP = "default-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// UI serves the built app from dist.
type UI struct {
	api      *api.API
	dist     fs.FS
	index    []byte // nil when the UI is not built into this binary
	etag     string // index's
	manifest map[string]assetEntry
}

func New(a *api.API, dist fs.FS) *UI {
	u := &UI{api: a, dist: dist, manifest: loadManifest(dist)}
	if b, err := fs.ReadFile(dist, "index.html"); err == nil {
		u.index, u.etag = b, `"`+etagOf(b)+`"`
	}
	return u
}

// Index serves the app, for the API's resources asked for as HTML.
func (u *UI) Index() http.Handler {
	return secure(http.HandlerFunc(u.app))
}

// Register mounts the redirect for the CLI's inspect links and, as the
// catch-all, the app and its assets.
func (u *UI) Register(mux *http.ServeMux) {
	mux.Handle("GET /inspect/{callID}", secure(http.HandlerFunc(u.inspectRedirect)))
	mux.Handle("/", secure(http.HandlerFunc(u.serve)))
}

// inspectRedirect resolves the inspect link the CLI prints for each call
// (DESIGN.md §15) to the call's page. A call that is gone, or that the
// caller may not see, gets the app with a 404 status; its /inspect/ route
// says the call has expired.
func (u *UI) inspectRedirect(w http.ResponseWriter, r *http.Request) {
	if id, err := u.api.Identifier.Identify(r.Context(), r.RemoteAddr); err == nil {
		if c, ok := u.api.Inspect.Get(r.PathValue("callID")); ok {
			if _, _, visible := u.api.VisibleHook(c.HookID, id); visible {
				http.Redirect(w, r, "/hooks/"+url.PathEscape(string(c.HookID))+"/calls/"+url.PathEscape(c.ID), http.StatusSeeOther)
				return
			}
		}
	}
	u.writeIndex(w, r, http.StatusNotFound)
}

func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", CSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
